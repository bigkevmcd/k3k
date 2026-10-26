package cmds

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// kubeconfigOutFlags holds the flags controlling the standalone kubeconfig file written
// next to the context added to the user kubeconfig.
type kubeconfigOutFlags struct {
	out                   string
	noOut                 bool
	insecureSkipTLSVerify bool

	// Deprecated: use out instead. Only registered on `kubeconfig generate`.
	configName string
}

// CobraFlagKubeconfigOut registers the flags controlling the standalone kubeconfig file.
func CobraFlagKubeconfigOut(cmd *cobra.Command, flags *kubeconfigOutFlags) {
	cmd.Flags().StringVar(&flags.out, "out", "", "also write a standalone kubeconfig of the cluster to this path")
	cmd.Flags().BoolVar(&flags.noOut, "no-out", false, "do not write a standalone kubeconfig file")

	cmd.Flags().BoolVar(&flags.insecureSkipTLSVerify, "kubeconfig-insecure-skip-tls-verify", false,
		"generate a kubeconfig that skips the verification of the server certificate, without the certificate authority data (insecure, for testing only)")

	cmd.MarkFlagsMutuallyExclusive("out", "no-out")

	if err := cmd.MarkFlagFilename("out"); err != nil {
		logrus.Fatal(err)
	}
}

// standalonePath returns the path of the standalone kubeconfig file to write for the
// cluster, or an empty string if no file should be written.
func (f *kubeconfigOutFlags) standalonePath(cluster *v1beta1.Cluster) string {
	switch {
	case f.noOut:
		return ""
	case f.out != "":
		return f.out
	case f.configName != "":
		// cobra already warned about the deprecation of the flag
		return f.configName
	}

	logrus.Warn("Writing a kubeconfig to the current directory is deprecated and will be removed in a future release. " +
		"The cluster is now added as a context to your kubeconfig. " +
		"Use --out to keep writing a standalone kubeconfig, or --no-out to silence this warning.")

	return cluster.Namespace + "-" + cluster.Name + "-kubeconfig.yaml"
}

// applyTLS strips the certificate authority data from the clusters of the config and makes
// them skip the verification of the server certificate, when requested.
func (f *kubeconfigOutFlags) applyTLS(config *clientcmdapi.Config) {
	if !f.insecureSkipTLSVerify {
		return
	}

	logrus.Warn("The generated kubeconfig skips the verification of the server certificate: use it for testing only.")

	for _, cluster := range config.Clusters {
		cluster.CertificateAuthority = ""
		cluster.CertificateAuthorityData = nil
		cluster.InsecureSkipTLSVerify = true
	}
}
