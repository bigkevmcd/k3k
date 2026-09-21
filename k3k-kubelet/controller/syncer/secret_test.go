package syncer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestSecretSyncerName(t *testing.T) {
	syncer := &SecretSyncer{}
	assert.Equal(t, secretControllerName, syncer.Name())
}

func TestSecretSyncerTranslateSecret(t *testing.T) {
	syncer := &SecretSyncer{
		Context: &Context{
			Translator: translate.ToHostTranslator{
				ClusterName:      testClusterName,
				ClusterNamespace: testClusterNamespace,
			},
		}}
	virtualSecret := newTestSecret("settings", map[string]string{"team": "platform"})

	hostSecret := syncer.translateSecret(virtualSecret)

	assert.Equal(t, syncer.Translator.TranslateName(virtualNamespace, "settings"), hostSecret.Name)
	assert.Equal(t, testClusterNamespace, hostSecret.Namespace)
	assert.Equal(t, []byte("value"), hostSecret.Data["key"])
	assert.Equal(t, "settings", hostSecret.Annotations[translate.ResourceNameAnnotation])
	assert.Equal(t, virtualNamespace, hostSecret.Annotations[translate.ResourceNamespaceAnnotation])
	assert.Equal(t, testClusterName, hostSecret.Labels[translate.ClusterNameLabel])
	assert.Equal(t, "settings", virtualSecret.Name)
	assert.Equal(t, virtualNamespace, virtualSecret.Namespace)
}

func TestSecretSyncerFilterResources(t *testing.T) {
	Secret := newTestSecret("settings", map[string]string{"environment": "production"})

	tests := []struct {
		name       string
		syncConfig v1beta1.SecretSyncConfig
		object     client.Object
		filtered   bool
	}{
		{
			name: "enabled with no selector",
			syncConfig: v1beta1.SecretSyncConfig{
				Enabled: true,
			},
			object:   Secret,
			filtered: true,
		},
		{
			name: "enabled matching selector",
			syncConfig: v1beta1.SecretSyncConfig{
				Enabled:  true,
				Selector: map[string]string{"environment": "production"},
			},
			object:   Secret,
			filtered: true,
		},
		{
			name: "enabled non-matching selector",
			syncConfig: v1beta1.SecretSyncConfig{
				Enabled:  true,
				Selector: map[string]string{"environment": "staging"},
			},
			object:   Secret,
			filtered: false,
		},
		{
			name: "enabled matching requirements",
			syncConfig: v1beta1.SecretSyncConfig{
				Enabled: true,
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"production", "staging"},
					},
				},
			},
			object:   Secret,
			filtered: true,
		},
		{
			name: "enabled non-matching requirements",
			syncConfig: v1beta1.SecretSyncConfig{
				Enabled: true,
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: metav1.LabelSelectorOpNotIn,
						Values:   []string{"production", "staging"},
					},
				},
			},
			object:   Secret,
			filtered: false,
		},
		{
			name:       "disabled non-deletion",
			syncConfig: v1beta1.SecretSyncConfig{},
			object:     Secret,
			filtered:   false,
		},
		{
			name:       "disabled deletion",
			syncConfig: v1beta1.SecretSyncConfig{},
			object: func() client.Object {
				deleted := Secret.DeepCopy()
				deletionTime := metav1.NewTime(time.Now())
				deleted.DeletionTimestamp = &deletionTime

				return deleted
			}(),
			filtered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := newSecretSyncer(t, newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.Sync.Secrets = tt.syncConfig
			}), nil)
			assert.Equal(t, tt.filtered, syncer.filterResources(tt.object))
		})
	}
}

func TestSecretSyncerReconcile(t *testing.T) {
	virtualObject := newTestSecret("settings", nil)
	cluster := newTestCluster(func(c *v1beta1.Cluster) {
		c.Spec.Sync.Secrets = v1beta1.SecretSyncConfig{Enabled: true}
	})
	syncer := newSecretSyncer(t, cluster, []client.Object{virtualObject})
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(virtualObject)}

	result, err := syncer.Reconcile(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)

	var gotVirtual corev1.Secret
	require.NoError(t, syncer.VirtualClient.Get(t.Context(), request.NamespacedName, &gotVirtual))
	assert.Contains(t, gotVirtual.Finalizers, secretFinalizerName)

	hostKey := syncer.Translator.NamespacedName(virtualObject)

	var gotHost corev1.Secret
	require.NoError(t, syncer.HostClient.Get(t.Context(), hostKey, &gotHost))
	assert.Equal(t, virtualObject.Data, gotHost.Data)
	assert.Equal(t, cluster.UID, gotHost.OwnerReferences[0].UID)

	gotVirtual.Data["key"] = []byte("updated")
	require.NoError(t, syncer.VirtualClient.Update(t.Context(), &gotVirtual))
	_, err = syncer.Reconcile(t.Context(), request)
	require.NoError(t, err)
	require.NoError(t, syncer.HostClient.Get(t.Context(), hostKey, &gotHost))
	assert.Equal(t, []byte("updated"), gotHost.Data["key"])
}

func TestSecretSyncerReconcileNotFound(t *testing.T) {
	syncer := newSecretSyncer(t, newTestCluster(func(c *v1beta1.Cluster) {
		c.Spec.Sync.Secrets = v1beta1.SecretSyncConfig{Enabled: true}
	}), nil)

	_, err := syncer.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "missing", Namespace: virtualNamespace}})
	require.NoError(t, err)
}

func newSecretSyncer(t *testing.T, cluster *v1beta1.Cluster, virtualObjects []client.Object, hostObjects ...client.Object) *SecretSyncer {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	hostObjects = append(hostObjects, cluster)

	return &SecretSyncer{
		Context: &Context{
			VirtualClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(virtualObjects...).Build(),
			HostClient:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostObjects...).Build(),
			Translator: translate.ToHostTranslator{
				ClusterName:      testClusterName,
				ClusterNamespace: testClusterNamespace,
			},
			ClusterName:      testClusterName,
			ClusterNamespace: testClusterNamespace,
		},
	}
}
func newTestSecret(name string, labels map[string]string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: virtualNamespace,
			Labels:    labels,
		},
		Data: map[string][]byte{"key": []byte("value")},
	}
}
