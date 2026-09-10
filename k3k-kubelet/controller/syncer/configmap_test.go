package syncer

import (
	"context"
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

const (
	testClusterName      = "my-cluster"
	testClusterNamespace = "host-ns"
	virtualNamespace     = "virtual-ns"
)

func TestConfigMapSyncerName(t *testing.T) {
	syncer := &ConfigMapSyncer{}
	assert.Equal(t, configMapControllerName, syncer.Name())
}

func TestConfigMapSyncerTranslateConfigMap(t *testing.T) {
	syncer := &ConfigMapSyncer{SyncerContext: &SyncerContext{
		Translator: translate.ToHostTranslator{
			ClusterName:      testClusterName,
			ClusterNamespace: testClusterNamespace,
		},
	}}
	virtualConfigMap := newTestConfigMap("settings", map[string]string{"team": "platform"})

	hostConfigMap := syncer.translateConfigMap(virtualConfigMap)

	assert.Equal(t, syncer.Translator.TranslateName(virtualNamespace, "settings"), hostConfigMap.Name)
	assert.Equal(t, testClusterNamespace, hostConfigMap.Namespace)
	assert.Equal(t, "value", hostConfigMap.Data["key"])
	assert.Equal(t, "settings", hostConfigMap.Annotations[translate.ResourceNameAnnotation])
	assert.Equal(t, virtualNamespace, hostConfigMap.Annotations[translate.ResourceNamespaceAnnotation])
	assert.Equal(t, testClusterName, hostConfigMap.Labels[translate.ClusterNameLabel])
	assert.Equal(t, "settings", virtualConfigMap.Name)
	assert.Equal(t, virtualNamespace, virtualConfigMap.Namespace)
}

func TestConfigMapSyncerFilterResources(t *testing.T) {
	configMap := newTestConfigMap("settings", map[string]string{"environment": "production"})

	tests := []struct {
		name       string
		syncConfig v1beta1.ConfigMapSyncConfig
		object     client.Object
		filtered   bool
	}{
		{
			name: "enabled with no selector",
			syncConfig: v1beta1.ConfigMapSyncConfig{
				Enabled: true,
			},
			object:   configMap,
			filtered: true,
		},
		{
			name: "enabled matching selector",
			syncConfig: v1beta1.ConfigMapSyncConfig{
				Enabled:  true,
				Selector: map[string]string{"environment": "production"},
			},
			object:   configMap,
			filtered: true,
		},
		{
			name: "enabled non-matching selector",
			syncConfig: v1beta1.ConfigMapSyncConfig{
				Enabled:  true,
				Selector: map[string]string{"environment": "staging"},
			},
			object:   configMap,
			filtered: false,
		},
		{
			name: "enabled matching requirements",
			syncConfig: v1beta1.ConfigMapSyncConfig{
				Enabled: true,
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"production", "staging"},
					},
				},
			},
			object:   configMap,
			filtered: true,
		},
		{
			name: "enabled non-matching requirements",
			syncConfig: v1beta1.ConfigMapSyncConfig{
				Enabled: true,
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: metav1.LabelSelectorOpNotIn,
						Values:   []string{"production", "staging"},
					},
				},
			},
			object:   configMap,
			filtered: false,
		},
		{
			name:       "disabled non-deletion",
			syncConfig: v1beta1.ConfigMapSyncConfig{},
			object:     configMap,
			filtered:   false,
		},
		{
			name:       "disabled deletion",
			syncConfig: v1beta1.ConfigMapSyncConfig{},
			object: func() client.Object {
				deleted := configMap.DeepCopy()
				deletionTime := metav1.NewTime(time.Now())
				deleted.DeletionTimestamp = &deletionTime

				return deleted
			}(),
			filtered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := newConfigMapSyncer(t, newTestCluster(tt.syncConfig), nil)
			assert.Equal(t, tt.filtered, syncer.filterResources(tt.object))
		})
	}
}

func TestConfigMapSyncerReconcile(t *testing.T) {
	virtualObject := newTestConfigMap("settings", nil)
	cluster := newTestCluster(v1beta1.ConfigMapSyncConfig{Enabled: true})
	syncer := newConfigMapSyncer(t, cluster, []client.Object{virtualObject})
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(virtualObject)}

	result, err := syncer.Reconcile(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)

	var gotVirtual corev1.ConfigMap
	require.NoError(t, syncer.VirtualClient.Get(context.Background(), request.NamespacedName, &gotVirtual))
	assert.Contains(t, gotVirtual.Finalizers, configMapFinalizerName)

	hostKey := syncer.Translator.NamespacedName(virtualObject)

	var gotHost corev1.ConfigMap
	require.NoError(t, syncer.HostClient.Get(context.Background(), hostKey, &gotHost))
	assert.Equal(t, virtualObject.Data, gotHost.Data)
	assert.Equal(t, cluster.UID, gotHost.OwnerReferences[0].UID)

	gotVirtual.Data["key"] = "updated"
	require.NoError(t, syncer.VirtualClient.Update(context.Background(), &gotVirtual))
	_, err = syncer.Reconcile(context.Background(), request)
	require.NoError(t, err)
	require.NoError(t, syncer.HostClient.Get(context.Background(), hostKey, &gotHost))
	assert.Equal(t, "updated", gotHost.Data["key"])
}

func TestConfigMapSyncerReconcileNotFound(t *testing.T) {
	syncer := newConfigMapSyncer(t, newTestCluster(v1beta1.ConfigMapSyncConfig{Enabled: true}), nil)

	_, err := syncer.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "missing", Namespace: virtualNamespace}})
	require.NoError(t, err)
}

func newConfigMapSyncer(t *testing.T, cluster *v1beta1.Cluster, virtualObjects []client.Object, hostObjects ...client.Object) *ConfigMapSyncer {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	hostObjects = append(hostObjects, cluster)

	return &ConfigMapSyncer{
		SyncerContext: &SyncerContext{
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

func newTestCluster(syncConfig v1beta1.ConfigMapSyncConfig) *v1beta1.Cluster {
	return &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: testClusterNamespace,
			UID:       types.UID("cluster-uid"),
		},
		Spec: v1beta1.ClusterSpec{
			Sync: &v1beta1.SyncConfig{ConfigMaps: syncConfig},
		},
	}
}

func newTestConfigMap(name string, labels map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: virtualNamespace,
			Labels:    labels,
		},
		Data: map[string]string{"key": "value"},
	}
}
