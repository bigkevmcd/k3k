package provider

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// testSetup provides common test setup for ConfigureNode tests
type testSetup struct {
	scheme         *runtime.Scheme
	hostClient     client.Client
	virtualClient  client.Client
	virtualCluster v1beta1.Cluster
	logger         logr.Logger
}

func TestConfigureNode_MirrorHostNodes_Labels(t *testing.T) {
	tests := map[string]struct {
		nodeName        string
		hostNodeLabels  map[string]string
		expectedLabels  map[string]string
		mirrorHostNodes bool
	}{
		"mirror host node labels when mirrorHostNodes is true": {
			nodeName: "test-node",
			hostNodeLabels: map[string]string{
				"kubernetes.io/hostname":                "test-node",
				"kubernetes.io/os":                      "linux",
				"kubernetes.io/arch":                    "amd64",
				"node.kubernetes.io/instance-type":      "m5.large",
				"topology.kubernetes.io/region":         "us-west-2",
				"topology.kubernetes.io/zone":           "us-west-2a",
				"custom-label":                          "custom-value",
				"node-role.kubernetes.io/control-plane": "true",
				"node-role.kubernetes.io/master":        "true",
			},
			expectedLabels: map[string]string{
				"kubernetes.io/hostname":                "test-node",
				"kubernetes.io/os":                      "linux",
				"kubernetes.io/arch":                    "amd64",
				"node.kubernetes.io/instance-type":      "m5.large",
				"topology.kubernetes.io/region":         "us-west-2",
				"topology.kubernetes.io/zone":           "us-west-2a",
				"custom-label":                          "custom-value",
				"node-role.kubernetes.io/control-plane": "true",
				"node-role.kubernetes.io/master":        "true",
			},
			mirrorHostNodes: true,
		},
		"mirror host node with minimal labels": {
			nodeName: "minimal-node",
			hostNodeLabels: map[string]string{
				"kubernetes.io/hostname": "minimal-node",
			},
			expectedLabels: map[string]string{
				"kubernetes.io/hostname": "minimal-node",
			},
			mirrorHostNodes: true,
		},
		"mirror host node with empty labels": {
			nodeName:        "empty-labels-node",
			hostNodeLabels:  map[string]string{},
			expectedLabels:  nil,
			mirrorHostNodes: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// Create host node with labels
			hostNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   tt.nodeName,
					Labels: tt.hostNodeLabels,
				},
				Spec: corev1.NodeSpec{
					PodCIDR: "10.244.0.0/24",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{},
				},
			}

			setup := newTestSetup(t, hostNode)

			// Create the node to be configured
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.nodeName,
					Labels: map[string]string{
						"initial-label": "initial-value",
					},
				},
			}

			// Call ConfigureNode
			ConfigureNode(setup.logger, node, "test-hostname", 10250, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", tt.mirrorHostNodes)

			// Assert labels are correctly mirrored
			assert.Equal(t, tt.expectedLabels, node.Labels, "Labels should be mirrored from host node")
		})
	}
}

func TestConfigureNode_MirrorHostNodes_Annotations(t *testing.T) {
	hostAnnotations := map[string]string{
		"node.alpha.kubernetes.io/ttl":                           "0",
		"volumes.kubernetes.io/controller-managed-attach-detach": "true",
		"custom-annotation":                                      "custom-value",
	}

	// Create host node with annotations
	hostNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-node",
			Annotations: hostAnnotations,
		},
		Spec: corev1.NodeSpec{
			PodCIDR: "10.244.0.0/24",
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{},
		},
	}

	setup := newTestSetup(t, hostNode)

	// Create the node to be configured
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-node",
			Annotations: map[string]string{},
		},
	}

	// Call ConfigureNode with mirrorHostNodes = true
	ConfigureNode(setup.logger, node, "test-hostname", 10250, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", true)

	// Assert annotations are correctly mirrored
	assert.Equal(t, hostAnnotations, node.Annotations, "Annotations should be mirrored from host node")
}

func TestConfigureNode_MirrorHostNodes_HostNodeNotFound(t *testing.T) {
	// Create fake clients WITHOUT the host node (empty client)
	setup := newTestSetup(t)

	// Create the node to be configured with some initial labels
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nonexistent-node",
			Labels: map[string]string{
				"initial-label": "initial-value",
			},
		},
	}

	servicePort := 30250

	// Call ConfigureNode with mirrorHostNodes = true but host node doesn't exist
	ConfigureNode(setup.logger, node, "test-hostname", servicePort, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", true)

	// When host node is not found, it copies from an empty/zero-valued node
	// The node should have nil labels, annotations, and empty spec/status (except the port)
	assert.Nil(t, node.Labels, "Labels should be nil when host node not found")
	assert.Nil(t, node.Annotations, "Annotations should be nil when host node not found")
	assert.Nil(t, node.Finalizers, "Finalizers should be nil when host node not found")

	// The service port should still be set
	assert.Equal(t, int32(servicePort), node.Status.DaemonEndpoints.KubeletEndpoint.Port, "Service port should still be set")

	// Spec should be empty
	assert.Empty(t, node.Spec.PodCIDR, "PodCIDR should be empty when host node not found")
	assert.Empty(t, node.Spec.PodCIDRs, "PodCIDRs should be empty when host node not found")
	assert.Empty(t, node.Spec.ProviderID, "ProviderID should be empty when host node not found")
}

func TestConfigureNode_MirrorHostNodes_Spec(t *testing.T) {
	// Create host node with specific spec
	hostNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Spec: corev1.NodeSpec{
			PodCIDR:    "10.244.0.0/24",
			PodCIDRs:   []string{"10.244.0.0/24"},
			ProviderID: "aws:///us-west-2a/i-1234567890abcdef0",
			Taints: []corev1.Taint{
				{
					Key:    "node.kubernetes.io/disk-pressure",
					Value:  "true",
					Effect: corev1.TaintEffectNoSchedule,
				},
			},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{},
			DaemonEndpoints: corev1.NodeDaemonEndpoints{
				KubeletEndpoint: corev1.DaemonEndpoint{
					Port: 10250,
				},
			},
		},
	}

	setup := newTestSetup(t, hostNode)

	// Create the node to be configured
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	servicePort := 30250

	// Call ConfigureNode with mirrorHostNodes = true
	ConfigureNode(setup.logger, node, "test-hostname", servicePort, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", true)

	// Assert spec is correctly mirrored
	assert.Equal(t, "10.244.0.0/24", node.Spec.PodCIDR, "PodCIDR should be mirrored")
	assert.Equal(t, []string{"10.244.0.0/24"}, node.Spec.PodCIDRs, "PodCIDRs should be mirrored")
	assert.Equal(t, "aws:///us-west-2a/i-1234567890abcdef0", node.Spec.ProviderID, "ProviderID should be mirrored")
	assert.Equal(t, 1, len(node.Spec.Taints), "Taints should be mirrored")
	assert.Equal(t, "node.kubernetes.io/disk-pressure", node.Spec.Taints[0].Key, "Taint key should be mirrored")

	// Assert that the service port is correctly set (should override the mirrored port)
	assert.Equal(t, int32(servicePort), node.Status.DaemonEndpoints.KubeletEndpoint.Port, "Service port should be set to the provided value")
}

func TestConfigureNode_NoMirrorHostNodes_Labels(t *testing.T) {
	setup := newTestSetup(t)

	// Create the node to be configured with some existing labels
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"existing-label": "existing-value",
			},
		},
	}

	// Call ConfigureNode with mirrorHostNodes = false
	ConfigureNode(setup.logger, node, "test-hostname", 10250, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", false)

	// Assert the required labels are set
	assert.Equal(t, "true", node.Labels["node.kubernetes.io/exclude-from-external-load-balancers"], "Should have exclude-from-external-load-balancers label")
	assert.Equal(t, "linux", node.Labels["kubernetes.io/os"], "Should have os label set to linux")
	assert.Equal(t, "existing-value", node.Labels["existing-label"], "Should preserve existing labels")
	assert.Equal(t, 3, len(node.Labels), "Should have 3 labels total")
}

func TestConfigureNode_NoMirrorHostNodes_Addresses(t *testing.T) {
	setup := newTestSetup(t)

	// Create the node to be configured
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
	}

	hostname := "my-test-host"
	ip := "10.0.0.5"

	// Call ConfigureNode with mirrorHostNodes = false
	ConfigureNode(setup.logger, node, hostname, 10250, ip, setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", false)

	// Assert addresses are correctly set
	assert.Equal(t, 2, len(node.Status.Addresses), "Should have 2 addresses")

	// Check hostname address
	var hostnameAddr, internalIPAddr *corev1.NodeAddress
	for i := range node.Status.Addresses {
		if node.Status.Addresses[i].Type == corev1.NodeHostName {
			hostnameAddr = &node.Status.Addresses[i]
		}
		if node.Status.Addresses[i].Type == corev1.NodeInternalIP {
			internalIPAddr = &node.Status.Addresses[i]
		}
	}

	assert.NotNil(t, hostnameAddr, "Should have hostname address")
	assert.Equal(t, hostname, hostnameAddr.Address, "Hostname address should match")

	assert.NotNil(t, internalIPAddr, "Should have internal IP address")
	assert.Equal(t, ip, internalIPAddr.Address, "Internal IP address should match")
}

func TestConfigureNode_NoMirrorHostNodes_Conditions(t *testing.T) {
	setup := newTestSetup(t)

	// Create the node to be configured
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
	}

	// Call ConfigureNode with mirrorHostNodes = false
	ConfigureNode(setup.logger, node, "test-hostname", 10250, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", false)

	// Assert conditions are set correctly
	assert.Equal(t, 5, len(node.Status.Conditions), "Should have 5 conditions")

	expectedConditions := map[string]corev1.ConditionStatus{
		"Ready":              corev1.ConditionTrue,
		"OutOfDisk":          corev1.ConditionFalse,
		"MemoryPressure":     corev1.ConditionFalse,
		"DiskPressure":       corev1.ConditionFalse,
		"NetworkUnavailable": corev1.ConditionFalse,
	}

	for _, condition := range node.Status.Conditions {
		expectedStatus, found := expectedConditions[string(condition.Type)]
		assert.True(t, found, "Unexpected condition type: %s", condition.Type)
		assert.Equal(t, expectedStatus, condition.Status, "Condition %s should have status %s", condition.Type, expectedStatus)
	}

	// Verify Ready condition specifically
	var readyCondition *corev1.NodeCondition
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == "Ready" {
			readyCondition = &node.Status.Conditions[i]
			break
		}
	}
	assert.NotNil(t, readyCondition, "Should have Ready condition")
	assert.Equal(t, "KubeletReady", readyCondition.Reason, "Ready condition should have correct reason")
	assert.Equal(t, "kubelet is ready.", readyCondition.Message, "Ready condition should have correct message")
}

func TestConfigureNode_NoMirrorHostNodes_Version(t *testing.T) {
	setup := newTestSetup(t)

	// Create the node to be configured
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
	}

	version := "v1.29.2"

	// Call ConfigureNode with mirrorHostNodes = false
	ConfigureNode(setup.logger, node, "test-hostname", 10250, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, version, false)

	// Assert version is correctly set
	assert.Equal(t, version, node.Status.NodeInfo.KubeletVersion, "KubeletVersion should be set correctly")
}

func TestConfigureNode_NoMirrorHostNodes_DaemonEndpoint(t *testing.T) {
	setup := newTestSetup(t)

	// Create the node to be configured
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
	}

	servicePort := 30250

	// Call ConfigureNode with mirrorHostNodes = false
	ConfigureNode(setup.logger, node, "test-hostname", servicePort, "192.168.1.100", setup.hostClient, setup.virtualClient, setup.virtualCluster, "v1.28.0", false)

	// Assert daemon endpoint port is correctly set
	assert.Equal(t, int32(servicePort), node.Status.DaemonEndpoints.KubeletEndpoint.Port, "Kubelet endpoint port should be set correctly")
}

// newTestSetup creates a new test setup with the given host node objects
func newTestSetup(t *testing.T, hostObjects ...client.Object) *testSetup {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = v1beta1.AddToScheme(scheme)
	assert.NoError(t, err)

	return &testSetup{
		scheme:        scheme,
		hostClient:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostObjects...).Build(),
		virtualClient: fake.NewClientBuilder().WithScheme(scheme).Build(),
		virtualCluster: v1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		},
		logger: zapr.NewLogger(zap.NewNop()),
	}
}
