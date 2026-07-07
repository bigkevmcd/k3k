package syncer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	testConfigMapClusterName      = "mycluster"
	testConfigMapClusterNamespace = "k3k-mycluster"
	testConfigMapVirtualNamespace = "test"
	testConfigMapVirtualName      = "my-configmap"
)

func TestConfigMapSyncerReconcile(t *testing.T) {
	scheme := newConfigMapTestScheme(t)

	translator := translate.ToHostTranslator{
		ClusterName:      testConfigMapClusterName,
		ClusterNamespace: testConfigMapClusterNamespace,
	}
	translatedName := translator.TranslateName(testConfigMapVirtualNamespace, testConfigMapVirtualName)

	tests := map[string]struct {
		virtualConfigMap *corev1.ConfigMap
		// hostObjects are objects pre-populated in the host cluster (besides the Cluster itself)
		hostObjects []client.Object
		// assertions
		wantErr                 bool
		checkHostConfigMapExist bool
		checkHostConfigMapGone  bool
		wantFinalizers          []string
		wantImmutable           *bool
		checkData               map[string]string
		checkLabels             map[string]string
	}{
		"creates configmap on host for first time and adds finalizer": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testConfigMapVirtualName,
					Namespace: testConfigMapVirtualNamespace,
				},
				Data: map[string]string{"key": "value"},
			},
			checkHostConfigMapExist: true,
			wantFinalizers:          []string{configMapFinalizerName},
			checkData:               map[string]string{"key": "value"},
		},
		"updates existing host configmap": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testConfigMapVirtualName,
					Namespace:  testConfigMapVirtualNamespace,
					Finalizers: []string{configMapFinalizerName},
				},
				Data: map[string]string{"key": "updated-value"},
			},
			hostObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testConfigMapClusterNamespace,
					},
					Data: map[string]string{"key": "old-value"},
				},
			},
			checkHostConfigMapExist: true,
			wantFinalizers:          []string{configMapFinalizerName},
			checkData:               map[string]string{"key": "updated-value"},
		},
		"deletes host configmap when virtual configmap is being deleted": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:              testConfigMapVirtualName,
					Namespace:         testConfigMapVirtualNamespace,
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{configMapFinalizerName},
				},
				Data: map[string]string{"key": "value"},
			},
			hostObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testConfigMapClusterNamespace,
					},
				},
			},
			checkHostConfigMapGone: true,
			wantFinalizers:         []string{},
		},
		"removes finalizer when host configmap is already gone": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:              testConfigMapVirtualName,
					Namespace:         testConfigMapVirtualNamespace,
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{configMapFinalizerName},
				},
			},
			wantFinalizers: []string{},
		},
		"immutable field is synced to host configmap": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testConfigMapVirtualName,
					Namespace: testConfigMapVirtualNamespace,
				},
				Data:      map[string]string{"key": "value"},
				Immutable: ptr.To(true),
			},
			checkHostConfigMapExist: true,
			wantFinalizers:          []string{configMapFinalizerName},
			wantImmutable:           ptr.To(true),
			checkData:               map[string]string{"key": "value"},
		},
		"updating immutable configmap should preserve immutability": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testConfigMapVirtualName,
					Namespace:  testConfigMapVirtualNamespace,
					Finalizers: []string{configMapFinalizerName},
				},
				Data:      map[string]string{"key": "value"},
				Immutable: ptr.To(true),
			},
			hostObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testConfigMapClusterNamespace,
					},
					Data:      map[string]string{"key": "value"},
					Immutable: ptr.To(true),
				},
			},
			checkHostConfigMapExist: true,
			wantFinalizers:          []string{configMapFinalizerName},
			wantImmutable:           ptr.To(true),
			checkData:               map[string]string{"key": "value"},
		},
		"cannot update data in immutable configmap": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testConfigMapVirtualName,
					Namespace:  testConfigMapVirtualNamespace,
					Finalizers: []string{configMapFinalizerName},
				},
				Data:      map[string]string{"key": "new-value"},
				Immutable: ptr.To(true),
			},
			hostObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testConfigMapClusterNamespace,
					},
					Data:      map[string]string{"key": "old-value"},
					Immutable: ptr.To(true),
				},
			},
			checkHostConfigMapExist: true,
			wantFinalizers:          []string{configMapFinalizerName},
			wantImmutable:           ptr.To(true),
			// The data should remain unchanged since immutable configmaps cannot be updated
			checkData: map[string]string{"key": "old-value"},
		},
		"can update metadata on immutable configmap": {
			virtualConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testConfigMapVirtualName,
					Namespace:  testConfigMapVirtualNamespace,
					Finalizers: []string{configMapFinalizerName},
					Labels:     map[string]string{"env": "production"},
				},
				Data:      map[string]string{"key": "value"},
				Immutable: ptr.To(true),
			},
			hostObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testConfigMapClusterNamespace,
						Labels: map[string]string{
							"k3k.io/clusterName": testConfigMapClusterName,
							"env":                "staging",
						},
					},
					Data:      map[string]string{"key": "value"},
					Immutable: ptr.To(true),
				},
			},
			checkHostConfigMapExist: true,
			wantFinalizers:          []string{configMapFinalizerName},
			wantImmutable:           ptr.To(true),
			checkData:               map[string]string{"key": "value"},
			checkLabels: map[string]string{
				"k3k.io/clusterName": testConfigMapClusterName,
				"env":                "production",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newConfigMapTestCluster(true, nil)

			hostObjs := append([]client.Object{cluster}, tt.hostObjects...)
			hostClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hostObjs...).
				Build()

			virtualClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.virtualConfigMap).
				Build()

			syncer := newConfigMapTestSyncer(hostClient, virtualClient)

			result, err := syncer.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testConfigMapVirtualName,
					Namespace: testConfigMapVirtualNamespace,
				},
			})

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, result)

			hostKey := types.NamespacedName{
				Name:      translatedName,
				Namespace: testConfigMapClusterNamespace,
			}

			if tt.checkHostConfigMapExist {
				var hostConfigMap corev1.ConfigMap
				require.NoError(t, hostClient.Get(context.Background(), hostKey, &hostConfigMap))
				if tt.checkData != nil {
					assert.Equal(t, tt.checkData, hostConfigMap.Data)
				}
				if tt.wantImmutable != nil {
					assert.Equal(t, tt.wantImmutable, hostConfigMap.Immutable)
				}
				if tt.checkLabels != nil {
					for key, value := range tt.checkLabels {
						assert.Equal(t, value, hostConfigMap.Labels[key], "label %s should match", key)
					}
				}
			}

			if tt.checkHostConfigMapGone {
				var hostConfigMap corev1.ConfigMap
				err := hostClient.Get(context.Background(), hostKey, &hostConfigMap)
				assert.True(t, apierrors.IsNotFound(err), "expected host configmap to be deleted")
			}

			virtKey := types.NamespacedName{
				Name:      testConfigMapVirtualName,
				Namespace: testConfigMapVirtualNamespace,
			}

			if tt.wantFinalizers != nil {
				var updatedConfigMap corev1.ConfigMap
				err := virtualClient.Get(context.Background(), virtKey, &updatedConfigMap)
				// The fake client may fully remove the object once all finalizers are
				// cleared and a DeletionTimestamp is present — an empty finalizer list
				// and NotFound are both valid outcomes for wantFinalizers == [].
				if err == nil {
					assert.Equal(t, tt.wantFinalizers, updatedConfigMap.Finalizers)
				} else {
					require.True(t, client.IgnoreNotFound(err) == nil, "unexpected error: %v", err)
					assert.Empty(t, tt.wantFinalizers, "expected object to exist but it was not found")
				}
			}
		})
	}
}

func TestConfigMapSyncerReconcileVirtualConfigMapNotFound(t *testing.T) {
	scheme := newConfigMapTestScheme(t)

	cluster := newConfigMapTestCluster(true, nil)
	hostClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	virtualClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	syncer := newConfigMapTestSyncer(hostClient, virtualClient)

	result, err := syncer.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "non-existent",
			Namespace: testConfigMapVirtualNamespace,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
}

func newConfigMapTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))
	return scheme
}

func newConfigMapTestCluster(configMapsEnabled bool, selector map[string]string) *v1beta1.Cluster {
	return &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testConfigMapClusterName,
			Namespace: testConfigMapClusterNamespace,
			UID:       "cluster-uid-1234",
		},
		Spec: v1beta1.ClusterSpec{
			Sync: &v1beta1.SyncConfig{
				ConfigMaps: v1beta1.ConfigMapSyncConfig{
					Enabled:  configMapsEnabled,
					Selector: selector,
				},
			},
		},
	}
}

func newConfigMapTestSyncer(hostClient, virtualClient client.Client) *ConfigMapSyncer {
	return &ConfigMapSyncer{
		SyncerContext: &SyncerContext{
			HostClient:    hostClient,
			VirtualClient: virtualClient,
			Translator: translate.ToHostTranslator{
				ClusterName:      testConfigMapClusterName,
				ClusterNamespace: testConfigMapClusterNamespace,
			},
			ClusterName:      testConfigMapClusterName,
			ClusterNamespace: testConfigMapClusterNamespace,
		},
	}
}
