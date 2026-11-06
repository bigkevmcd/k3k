package cmds

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// auditFlags holds the flags configuring the audit log of the K3s servers.
type auditFlags struct {
	audit       bool
	auditPolicy string
}

func (f *auditFlags) register(cmd *cobra.Command, usage string) {
	cmd.Flags().BoolVar(&f.audit, "audit", false, usage)
	cmd.Flags().StringVar(&f.auditPolicy, "audit-policy", "", "path to an audit.k8s.io/v1 Policy file for the audit log (implies --audit)")

	if err := cmd.MarkFlagFilename("audit-policy", "yaml", "yml"); err != nil {
		logrus.Fatal(err)
	}
}

// auditConfig returns the audit configuration from the flags, or nil if audit logging is not requested.
// The audit policy file, if any, is read and embedded in the configuration.
func (f *auditFlags) auditConfig() (*v1beta1.AuditConfig, error) {
	if !f.audit && f.auditPolicy == "" {
		return nil, nil
	}

	audit := &v1beta1.AuditConfig{Enabled: true}

	if f.auditPolicy != "" {
		policy, err := os.ReadFile(f.auditPolicy)
		if err != nil {
			return nil, fmt.Errorf("failed to read the audit policy: %w", err)
		}

		audit.Policy = string(policy)
	}

	return audit, nil
}
