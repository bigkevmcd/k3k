package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// ErrLoadBalancerNotReady is returned when the cluster is exposed through a LoadBalancer
// that has not been assigned an address yet.
var ErrLoadBalancerNotReady = errors.New("the LoadBalancer address of the cluster service has not been assigned yet")

// URL generates the API server URL for the kubeconfig based on the service configuration.
//
// It handles internal vs external access patterns:
//   - Internal access (hostServerIP == service.ClusterIP): uses the ClusterIP for direct pod-to-pod communication
//   - External access (hostServerIP != service.ClusterIP): uses the appropriate external endpoint based on the service type
//
// Service type handling:
//   - ClusterIP: uses service.Spec.ClusterIP (internal-only)
//   - NodePort: uses hostServerIP:NodePort for external access, ClusterIP:Port for internal access
//   - LoadBalancer: uses the first LoadBalancer ingress IP or hostname with the custom LoadBalancer port (if any)
//     for external access, returning ErrLoadBalancerNotReady if no address has been assigned yet,
//     and ClusterIP:Port for internal access
//   - Ingress (if configured): takes precedence over the service-based URL
//
// The hostServerIP parameter determines the access pattern:
//   - Controller reconciliation: passes service.Spec.ClusterIP → internal access
//   - CLI kubeconfig export: passes the external host → external access
//
// If the resulting host is not one of the TLS SANs of the cluster, it's replaced by the first TLS SAN
// of the spec, or of the status, so the server certificate can be verified.
func URL(ctx context.Context, c client.Client, cluster *v1beta1.Cluster, hostServerIP string) (*url.URL, error) {
	endpoint, err := ResolveEndpoint(ctx, c, cluster, hostServerIP)
	if err != nil {
		return nil, err
	}

	return endpoint.VerifiableURL(), nil
}

// Endpoint is the address of the API server of a virtual cluster.
type Endpoint struct {
	// URL is the API server URL.
	URL *url.URL

	// TLSServerName is the TLS SAN to verify the server certificate against, set only when
	// the URL host is not one of the TLS SANs of the cluster.
	TLSServerName string

	port int32
}

// VerifiableURL returns the URL with its host replaced by the TLSServerName, if any,
// so the server certificate can be verified without a tls-server-name.
func (e Endpoint) VerifiableURL() *url.URL {
	if e.TLSServerName == "" {
		return e.URL
	}

	return buildURL(e.TLSServerName, e.port)
}

// ResolveEndpoint returns the API server endpoint like URL, but it always keeps the host in the URL.
// When the host is not one of the TLS SANs of the cluster, the TLS SAN that URL would replace it with
// is returned as the TLSServerName, so clients can still verify the server certificate.
func ResolveEndpoint(ctx context.Context, c client.Client, cluster *v1beta1.Cluster, hostServerIP string) (Endpoint, error) {
	log := ctrl.LoggerFrom(ctx)

	key := types.NamespacedName{
		Name:      ServiceName(cluster.Name),
		Namespace: cluster.Namespace,
	}

	// Check if ingress is configured
	if cluster.Spec.Expose != nil && cluster.Spec.Expose.Ingress != nil {
		key := types.NamespacedName{
			Name:      IngressName(cluster.Name),
			Namespace: cluster.Namespace,
		}

		var k3kIngress networkingv1.Ingress
		if err := c.Get(ctx, key, &k3kIngress); err != nil {
			return Endpoint{}, err
		}

		if len(k3kIngress.Spec.Rules) > 0 && k3kIngress.Spec.Rules[0].Host != "" {
			return Endpoint{
				URL: &url.URL{
					Scheme: "https",
					Host:   k3kIngress.Spec.Rules[0].Host,
				},
			}, nil
		}

		log.V(1).Info("Ingress has no rule with a host set, falling back to the service URL.")
	}

	// Fall back to Service-based URL
	var k3kService corev1.Service
	if err := c.Get(ctx, key, &k3kService); err != nil {
		return Endpoint{}, err
	}

	// init to hostServerIP and 443 port
	host := hostServerIP
	port := int32(443)

	// Use service port as default if available
	if len(k3kService.Spec.Ports) > 0 {
		port = k3kService.Spec.Ports[0].Port
	}

	// Handle each service type separately
	switch k3kService.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		host = k3kService.Spec.ClusterIP

	case corev1.ServiceTypeNodePort:
		// Only use NodePort if hostServerIP is NOT the ClusterIP
		// If hostServerIP == ClusterIP, this is an internal connection, use ClusterIP
		if hostServerIP != k3kService.Spec.ClusterIP {
			if len(k3kService.Spec.Ports) > 0 {
				port = k3kService.Spec.Ports[0].NodePort
			}
		} else {
			// Internal connection: use ClusterIP
			host = k3kService.Spec.ClusterIP
		}

	case corev1.ServiceTypeLoadBalancer:
		// Internal connection: use ClusterIP and the default server port
		if hostServerIP == k3kService.Spec.ClusterIP {
			return newEndpoint(k3kService.Spec.ClusterIP, port), nil
		}

		// The LoadBalancer addresses are always added to the server TLS SANs by the controller,
		// so there is no need to check them: falling back to another SAN would mix it with the LoadBalancer port.
		sans := LoadBalancerSANs(&k3kService)
		if len(sans) == 0 {
			return Endpoint{}, ErrLoadBalancerNotReady
		}

		// use the custom LoadBalancer server port, if configured
		for _, servicePort := range k3kService.Spec.Ports {
			if servicePort.Name == lbServerPortName {
				port = servicePort.Port
			}
		}

		return newEndpoint(sans[0], port), nil

	default:
		// ExternalName services expose no routable address, so keep the host server IP
		log.V(1).Info("Unsupported service type, falling back to the host server IP", "type", k3kService.Spec.Type)
	}

	endpoint := newEndpoint(host, port)

	if !slices.Contains(cluster.Status.TLSSANs, host) {
		log.V(1).Info(fmt.Sprintf("IP %s not in tlsSANs.", host))

		if len(cluster.Spec.TLSSANs) > 0 {
			log.V(1).Info("Using the first TLS SAN in the spec as a fallback: " + cluster.Spec.TLSSANs[0])

			endpoint.TLSServerName = cluster.Spec.TLSSANs[0]
		} else if len(cluster.Status.TLSSANs) > 0 {
			log.V(1).Info("No explicit tlsSANs specified. Trying to use the first TLS SAN in the status: " + cluster.Status.TLSSANs[0])

			endpoint.TLSServerName = cluster.Status.TLSSANs[0]
		} else {
			log.V(1).Info("IP not found in tlsSANs. This could cause issue with the certificate validation.")
		}
	}

	return endpoint, nil
}

func newEndpoint(host string, port int32) Endpoint {
	return Endpoint{
		URL:  buildURL(host, port),
		port: port,
	}
}

// buildURL returns the https URL for the given host, with the port only if it's not the default HTTPS port.
func buildURL(host string, port int32) *url.URL {
	if port != int32(443) {
		host = net.JoinHostPort(host, strconv.Itoa(int(port)))
	}

	return &url.URL{
		Scheme: "https",
		Host:   host,
	}
}
