package cmds

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func writeAuditPolicy(t *testing.T, policy string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "audit-policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(policy), 0o600))

	return path
}

func Test_auditFlags_auditConfig(t *testing.T) {
	policyPath := writeAuditPolicy(t, "custom")

	tests := map[string]struct {
		flags   auditFlags
		want    *v1beta1.AuditConfig
		wantErr string
	}{
		"no flags": {},
		"audit enabled": {
			flags: auditFlags{audit: true},
			want:  &v1beta1.AuditConfig{Enabled: true},
		},
		"audit policy implies audit": {
			flags: auditFlags{auditPolicy: policyPath},
			want:  &v1beta1.AuditConfig{Enabled: true, Policy: "custom"},
		},
		"missing audit policy file": {
			flags:   auditFlags{auditPolicy: filepath.Join(t.TempDir(), "missing.yaml")},
			wantErr: "failed to read the audit policy",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := tt.flags.auditConfig()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_newCluster_Audit(t *testing.T) {
	cluster, err := newCluster("foo", "k3k-foo", &CreateConfig{servers: 1, auditFlags: auditFlags{audit: true}})
	require.NoError(t, err)
	assert.Equal(t, &v1beta1.AuditConfig{Enabled: true}, cluster.Spec.Audit)

	cluster, err = newCluster("foo", "k3k-foo", &CreateConfig{servers: 1})
	require.NoError(t, err)
	assert.Nil(t, cluster.Spec.Audit)
}

func TestUpdateCluster_Audit(t *testing.T) {
	policyPath := writeAuditPolicy(t, "custom")

	tests := map[string]struct {
		audit   *v1beta1.AuditConfig
		flags   map[string]string
		want    *v1beta1.AuditConfig
		wantErr string
	}{
		"enable audit": {
			flags: map[string]string{"audit": "true"},
			want:  &v1beta1.AuditConfig{Enabled: true},
		},
		"enable audit with a policy": {
			flags: map[string]string{"audit-policy": policyPath},
			want:  &v1beta1.AuditConfig{Enabled: true, Policy: "custom"},
		},
		"disable audit keeps the policy": {
			audit: &v1beta1.AuditConfig{Enabled: true, Policy: "custom"},
			flags: map[string]string{"audit": "false"},
			want:  &v1beta1.AuditConfig{Policy: "custom"},
		},
		"policy with audit disabled": {
			flags:   map[string]string{"audit": "false", "audit-policy": policyPath},
			wantErr: "--audit-policy cannot be used with --audit=false",
		},
		"empty policy path": {
			audit:   &v1beta1.AuditConfig{Enabled: true, Policy: "custom"},
			flags:   map[string]string{"audit-policy": ""},
			wantErr: "--audit-policy requires the path of a policy file",
		},
		"empty policy path with audit enabled": {
			flags:   map[string]string{"audit": "true", "audit-policy": ""},
			wantErr: "--audit-policy requires the path of a policy file",
		},
		"no audit flags": {
			audit: &v1beta1.AuditConfig{Enabled: true},
			flags: map[string]string{"servers": "3"},
			want:  &v1beta1.AuditConfig{Enabled: true},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "k3k-foo"},
				Spec:       v1beta1.ClusterSpec{Audit: tt.audit},
			}
			cl := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()
			appCtx := &AppContext{Client: cl}

			config := &UpdateConfig{}
			cmd := &cobra.Command{}
			updateFlags(cmd, config)

			require.NoError(t, cmd.Flags().Set("no-confirm", "true"))

			for flag, value := range tt.flags {
				require.NoError(t, cmd.Flags().Set(flag, value))
			}

			err := updateAction(appCtx, config)(cmd, []string{"foo"})
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			var updated v1beta1.Cluster
			require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(cluster), &updated))
			assert.Equal(t, tt.want, updated.Spec.Audit)
		})
	}
}

func Test_updateAudit_changes(t *testing.T) {
	policyPath := writeAuditPolicy(t, "custom")

	tests := map[string]struct {
		audit *v1beta1.AuditConfig
		flags map[string]string
		want  []change
	}{
		"enable audit": {
			flags: map[string]string{"audit": "true"},
			want:  []change{{"Audit", "false", "true"}},
		},
		"set a policy": {
			flags: map[string]string{"audit-policy": policyPath},
			want: []change{
				{"Audit", "false", "true"},
				{"Audit policy", "default", "custom (" + policyPath + ")"},
			},
		},
		"replace a policy": {
			audit: &v1beta1.AuditConfig{Enabled: true, Policy: "previous"},
			flags: map[string]string{"audit-policy": policyPath},
			want:  []change{{"Audit policy", "custom", "custom (" + policyPath + ")"}},
		},
		"same policy": {
			audit: &v1beta1.AuditConfig{Enabled: true, Policy: "custom"},
			flags: map[string]string{"audit-policy": policyPath},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := &v1beta1.Cluster{Spec: v1beta1.ClusterSpec{Audit: tt.audit}}

			config := &UpdateConfig{}
			cmd := &cobra.Command{}
			updateFlags(cmd, config)

			for flag, value := range tt.flags {
				require.NoError(t, cmd.Flags().Set(flag, value))
			}

			changes, err := updateAudit(cmd, config, cluster)
			require.NoError(t, err)
			assert.Equal(t, tt.want, changes)
		})
	}
}
