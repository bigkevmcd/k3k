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
	testSecretClusterName      = "mycluster"
	testSecretClusterNamespace = "k3k-mycluster"
	testSecretVirtualNamespace = "test"
	testSecretVirtualName      = "my-secret"
)

func TestSecretSyncerReconcile(t *testing.T) {
	scheme := newSecretTestScheme(t)

	translator := translate.ToHostTranslator{
		ClusterName:      testSecretClusterName,
		ClusterNamespace: testSecretClusterNamespace,
	}
	translatedName := translator.TranslateName(testSecretVirtualNamespace, testSecretVirtualName)

	tests := map[string]struct {
		virtualSecret *corev1.Secret
		// hostObjects are objects pre-populated in the host cluster (besides the Cluster itself)
		hostObjects []client.Object
		// assertions
		wantErr              bool
		checkHostSecretExist bool
		checkHostSecretGone  bool
		wantFinalizers       []string
		wantImmutable        *bool
		checkData            map[string][]byte
		checkLabels          map[string]string
	}{
		"creates secret on host for first time and adds finalizer": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testSecretVirtualName,
					Namespace: testSecretVirtualNamespace,
				},
				Data: map[string][]byte{"key": []byte("value")},
			},
			checkHostSecretExist: true,
			wantFinalizers:       []string{secretFinalizerName},
			checkData:            map[string][]byte{"key": []byte("value")},
		},
		"updates existing host secret": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testSecretVirtualName,
					Namespace:  testSecretVirtualNamespace,
					Finalizers: []string{secretFinalizerName},
				},
				Data: map[string][]byte{"key": []byte("updated-value")},
			},
			hostObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testSecretClusterNamespace,
					},
					Data: map[string][]byte{"key": []byte("old-value")},
				},
			},
			checkHostSecretExist: true,
			wantFinalizers:       []string{secretFinalizerName},
			checkData:            map[string][]byte{"key": []byte("updated-value")},
		},
		"deletes host secret when virtual secret is being deleted": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              testSecretVirtualName,
					Namespace:         testSecretVirtualNamespace,
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{secretFinalizerName},
				},
				Data: map[string][]byte{"key": []byte("value")},
			},
			hostObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testSecretClusterNamespace,
					},
				},
			},
			checkHostSecretGone: true,
			wantFinalizers:      []string{},
		},
		"removes finalizer when host secret is already gone": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              testSecretVirtualName,
					Namespace:         testSecretVirtualNamespace,
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{secretFinalizerName},
				},
			},
			wantFinalizers: []string{},
		},
		"immutable field is synced to host secret": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testSecretVirtualName,
					Namespace: testSecretVirtualNamespace,
				},
				Data:      map[string][]byte{"key": []byte("value")},
				Immutable: ptr.To(true),
			},
			checkHostSecretExist: true,
			wantFinalizers:       []string{secretFinalizerName},
			wantImmutable:        ptr.To(true),
			checkData:            map[string][]byte{"key": []byte("value")},
		},
		"updating immutable secret should preserve immutability": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testSecretVirtualName,
					Namespace:  testSecretVirtualNamespace,
					Finalizers: []string{secretFinalizerName},
				},
				Data:      map[string][]byte{"key": []byte("value")},
				Immutable: ptr.To(true),
			},
			hostObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testSecretClusterNamespace,
					},
					Data:      map[string][]byte{"key": []byte("value")},
					Immutable: ptr.To(true),
				},
			},
			checkHostSecretExist: true,
			wantFinalizers:       []string{secretFinalizerName},
			wantImmutable:        ptr.To(true),
			checkData:            map[string][]byte{"key": []byte("value")},
		},
		"cannot update data in immutable secret": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testSecretVirtualName,
					Namespace:  testSecretVirtualNamespace,
					Finalizers: []string{secretFinalizerName},
				},
				Data:      map[string][]byte{"key": []byte("new-value")},
				Immutable: ptr.To(true),
			},
			hostObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testSecretClusterNamespace,
					},
					Data:      map[string][]byte{"key": []byte("old-value")},
					Immutable: ptr.To(true),
				},
			},
			checkHostSecretExist: true,
			wantFinalizers:       []string{secretFinalizerName},
			wantImmutable:        ptr.To(true),
			// The data should remain unchanged since immutable secrets cannot be updated
			checkData: map[string][]byte{"key": []byte("old-value")},
		},
		"can update metadata on immutable secret": {
			virtualSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testSecretVirtualName,
					Namespace:  testSecretVirtualNamespace,
					Finalizers: []string{secretFinalizerName},
					Labels:     map[string]string{"env": "production"},
				},
				Data:      map[string][]byte{"key": []byte("value")},
				Immutable: ptr.To(true),
			},
			hostObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      translatedName,
						Namespace: testSecretClusterNamespace,
						Labels: map[string]string{
							"k3k.io/clusterName": testSecretClusterName,
							"env":                "staging",
						},
					},
					Data:      map[string][]byte{"key": []byte("value")},
					Immutable: ptr.To(true),
				},
			},
			checkHostSecretExist: true,
			wantFinalizers:       []string{secretFinalizerName},
			wantImmutable:        ptr.To(true),
			checkData:            map[string][]byte{"key": []byte("value")},
			checkLabels: map[string]string{
				"k3k.io/clusterName": testSecretClusterName,
				"env":                "production",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newSecretTestCluster(true, nil)

			hostObjs := append([]client.Object{cluster}, tt.hostObjects...)
			hostClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(hostObjs...).
				Build()

			virtualClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.virtualSecret).
				Build()

			syncer := newSecretTestSyncer(hostClient, virtualClient)

			result, err := syncer.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testSecretVirtualName,
					Namespace: testSecretVirtualNamespace,
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
				Namespace: testSecretClusterNamespace,
			}

			if tt.checkHostSecretExist {
				var hostSecret corev1.Secret
				require.NoError(t, hostClient.Get(context.Background(), hostKey, &hostSecret))
				if tt.checkData != nil {
					assert.Equal(t, tt.checkData, hostSecret.Data)
				}
				if tt.wantImmutable != nil {
					assert.Equal(t, tt.wantImmutable, hostSecret.Immutable)
				}
				if tt.checkLabels != nil {
					for key, value := range tt.checkLabels {
						assert.Equal(t, value, hostSecret.Labels[key], "label %s should match", key)
					}
				}
			}

			if tt.checkHostSecretGone {
				var hostSecret corev1.Secret
				err := hostClient.Get(context.Background(), hostKey, &hostSecret)
				assert.True(t, apierrors.IsNotFound(err), "expected host secret to be deleted")
			}

			virtKey := types.NamespacedName{
				Name:      testSecretVirtualName,
				Namespace: testSecretVirtualNamespace,
			}

			if tt.wantFinalizers != nil {
				var updatedSecret corev1.Secret
				err := virtualClient.Get(context.Background(), virtKey, &updatedSecret)
				// The fake client may fully remove the object once all finalizers are
				// cleared and a DeletionTimestamp is present — an empty finalizer list
				// and NotFound are both valid outcomes for wantFinalizers == [].
				if err == nil {
					assert.Equal(t, tt.wantFinalizers, updatedSecret.Finalizers)
				} else {
					require.True(t, client.IgnoreNotFound(err) == nil, "unexpected error: %v", err)
					assert.Empty(t, tt.wantFinalizers, "expected object to exist but it was not found")
				}
			}
		})
	}
}

func TestSecretSyncerReconcileVirtualSecretNotFound(t *testing.T) {
	scheme := newSecretTestScheme(t)

	cluster := newSecretTestCluster(true, nil)
	hostClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	virtualClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	syncer := newSecretTestSyncer(hostClient, virtualClient)

	result, err := syncer.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "non-existent",
			Namespace: testSecretVirtualNamespace,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
}

func TestSecretSyncerFilterResources(t *testing.T) {
	scheme := newSecretTestScheme(t)

	deletionTime := metav1.NewTime(time.Now())

	tests := map[string]struct {
		secretsEnabled bool
		selector       map[string]string
		object         client.Object
		clusterExists  bool
		wantFiltered   bool
	}{
		"sync enabled, empty selector allows all objects": {
			secretsEnabled: true,
			selector:       nil,
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "any-secret", Namespace: "test"},
			},
			clusterExists: true,
			wantFiltered:  true,
		},
		"sync enabled, selector matches object labels": {
			secretsEnabled: true,
			selector:       map[string]string{"env": "prod"},
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "labeled-secret",
					Namespace: "test",
					Labels:    map[string]string{"env": "prod"},
				},
			},
			clusterExists: true,
			wantFiltered:  true,
		},
		"sync enabled, selector does not match object labels": {
			secretsEnabled: true,
			selector:       map[string]string{"env": "prod"},
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unlabeled-secret",
					Namespace: "test",
					Labels:    map[string]string{"env": "staging"},
				},
			},
			clusterExists: true,
			wantFiltered:  false,
		},
		"sync disabled, object with no deletion timestamp": {
			secretsEnabled: false,
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "some-secret", Namespace: "test"},
			},
			clusterExists: true,
			wantFiltered:  false,
		},
		"sync disabled, object being deleted is allowed through for cleanup": {
			secretsEnabled: false,
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "being-deleted",
					Namespace:         "test",
					DeletionTimestamp: &deletionTime,
				},
			},
			clusterExists: true,
			wantFiltered:  true,
		},
		"cluster not found in host returns false": {
			secretsEnabled: true,
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "any-secret", Namespace: "test"},
			},
			clusterExists: false,
			wantFiltered:  false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var hostObjs []client.Object
			if tt.clusterExists {
				hostObjs = append(hostObjs, newSecretTestCluster(tt.secretsEnabled, tt.selector))
			}

			hostClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostObjs...).Build()
			virtualClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			syncer := newSecretTestSyncer(hostClient, virtualClient)

			got := syncer.filterResources(tt.object)
			assert.Equal(t, tt.wantFiltered, got)
		})
	}
}

func TestSecretSyncerTranslateSecret(t *testing.T) {
	scheme := newSecretTestScheme(t)
	hostClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	virtualClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	syncer := newSecretTestSyncer(hostClient, virtualClient)

	translator := translate.ToHostTranslator{
		ClusterName:      testSecretClusterName,
		ClusterNamespace: testSecretClusterNamespace,
	}

	tests := map[string]struct {
		input    *corev1.Secret
		wantType corev1.SecretType
	}{
		"ServiceAccountToken type is converted to Opaque": {
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testSecretVirtualName,
					Namespace: testSecretVirtualNamespace,
				},
				Type: corev1.SecretTypeServiceAccountToken,
				Data: map[string][]byte{"token": []byte("abc123")},
			},
			wantType: corev1.SecretTypeOpaque,
		},
		"Opaque type is preserved": {
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testSecretVirtualName,
					Namespace: testSecretVirtualNamespace,
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{"key": []byte("val")},
			},
			wantType: corev1.SecretTypeOpaque,
		},
		"TLS type is preserved": {
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testSecretVirtualName,
					Namespace: testSecretVirtualNamespace,
				},
				Type: corev1.SecretTypeTLS,
			},
			wantType: corev1.SecretTypeTLS,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := syncer.translateSecret(tt.input)

			assert.Equal(t, tt.wantType, result.Type)
			assert.Equal(t, tt.input.Data, result.Data)

			// The translated secret should be placed in the host cluster namespace
			assert.Equal(t, testSecretClusterNamespace, result.Namespace)
			assert.Equal(t, translator.TranslateName(testSecretVirtualNamespace, testSecretVirtualName), result.Name)

			// Translation annotations should be set
			assert.Equal(t, testSecretVirtualName, result.Annotations[translate.ResourceNameAnnotation])
			assert.Equal(t, testSecretVirtualNamespace, result.Annotations[translate.ResourceNamespaceAnnotation])

			// Cluster name label should be set
			assert.Equal(t, testSecretClusterName, result.Labels[translate.ClusterNameLabel])

			// The original secret should not be mutated
			assert.Equal(t, testSecretVirtualName, tt.input.Name)
			assert.Equal(t, testSecretVirtualNamespace, tt.input.Namespace)
		})
	}
}

func newSecretTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))
	return scheme
}

func newSecretTestCluster(secretsEnabled bool, selector map[string]string) *v1beta1.Cluster {
	return &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretClusterName,
			Namespace: testSecretClusterNamespace,
			UID:       "cluster-uid-1234",
		},
		Spec: v1beta1.ClusterSpec{
			Sync: &v1beta1.SyncConfig{
				Secrets: v1beta1.SecretSyncConfig{
					Enabled:  secretsEnabled,
					Selector: selector,
				},
			},
		},
	}
}

func newSecretTestSyncer(hostClient, virtualClient client.Client) *SecretSyncer {
	return &SecretSyncer{
		SyncerContext: &SyncerContext{
			HostClient:    hostClient,
			VirtualClient: virtualClient,
			Translator: translate.ToHostTranslator{
				ClusterName:      testSecretClusterName,
				ClusterNamespace: testSecretClusterNamespace,
			},
			ClusterName:      testSecretClusterName,
			ClusterNamespace: testSecretClusterNamespace,
		},
	}
}
