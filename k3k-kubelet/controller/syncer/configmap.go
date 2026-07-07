package syncer

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	configMapControllerName = "configmap-syncer"
	configMapFinalizerName  = "configmap.k3k.io/finalizer"
)

type ConfigMapSyncer struct {
	// SyncerContext contains all client information for host and virtual cluster
	*SyncerContext
}

func (c *ConfigMapSyncer) Name() string {
	return configMapControllerName
}

// AddConfigMapSyncer adds configmap syncer controller to the manager of the virtual cluster
func AddConfigMapSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := ConfigMapSyncer{
		SyncerContext: &SyncerContext{
			VirtualClient: virtMgr.GetClient(),
			HostClient:    hostMgr.GetClient(),
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, configMapControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&corev1.ConfigMap{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (c *ConfigMapSyncer) filterResources(object client.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := c.HostClient.Get(ctx, types.NamespacedName{Name: c.ClusterName, Namespace: c.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for configMap Sync Config
	syncConfig := cluster.Spec.Sync.ConfigMaps

	// If syncing is disabled, only process deletions to allow for cleanup.
	if !syncConfig.Enabled {
		return object.GetDeletionTimestamp() != nil
	}

	labelSelector := labels.SelectorFromSet(syncConfig.Selector)
	if labelSelector.Empty() {
		return true
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

// Reconcile implements reconcile.Reconciler and synchronizes the objects in objs to the host cluster
func (c *ConfigMapSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", c.ClusterName, "clusterNamespace", c.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var cluster v1beta1.Cluster

	if err := c.HostClient.Get(ctx, types.NamespacedName{Name: c.ClusterName, Namespace: c.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	var virtualConfigMap corev1.ConfigMap

	if err := c.VirtualClient.Get(ctx, req.NamespacedName, &virtualConfigMap); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	syncedConfigMap := c.translateConfigMap(&virtualConfigMap)

	if err := controllerutil.SetOwnerReference(&cluster, syncedConfigMap, c.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtualConfigMap.DeletionTimestamp.IsZero() {
		// deleting the synced configMap if exist
		if err := c.HostClient.Delete(ctx, syncedConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced configMap
		if controllerutil.RemoveFinalizer(&virtualConfigMap, configMapFinalizerName) {
			if err := c.VirtualClient.Update(ctx, &virtualConfigMap); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtualConfigMap, configMapFinalizerName) {
		if err := c.VirtualClient.Update(ctx, &virtualConfigMap); err != nil {
			return reconcile.Result{}, err
		}
	}

	var hostConfigMap corev1.ConfigMap
	if err := c.HostClient.Get(ctx, types.NamespacedName{Name: syncedConfigMap.Name, Namespace: syncedConfigMap.Namespace}, &hostConfigMap); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the ConfigMap for the first time on the host cluster")
			return reconcile.Result{}, c.HostClient.Create(ctx, syncedConfigMap)
		}

		return reconcile.Result{}, err
	}

	// TODO: Add option to keep labels/annotation set by the host cluster
	log.Info("updating ConfigMap on the host cluster")

	// Update the existing host configmap to preserve ResourceVersion and other metadata
	// Always update metadata (labels, annotations) as these can be changed even on immutable configmaps
	hostConfigMap.Labels = syncedConfigMap.Labels
	hostConfigMap.Annotations = syncedConfigMap.Annotations

	// Only update data and binaryData fields if the configmap is not immutable
	// Immutable configmaps cannot have their data modified after creation
	if hostConfigMap.Immutable == nil || !*hostConfigMap.Immutable {
		hostConfigMap.Data = syncedConfigMap.Data
		hostConfigMap.BinaryData = syncedConfigMap.BinaryData
		hostConfigMap.Immutable = syncedConfigMap.Immutable
	}

	return reconcile.Result{}, c.HostClient.Update(ctx, &hostConfigMap)
}

// translateConfigMap will translate a given configMap created in the virtual cluster and
// translates it to host cluster object
func (c *ConfigMapSyncer) translateConfigMap(configMap *corev1.ConfigMap) *corev1.ConfigMap {
	hostConfigMap := configMap.DeepCopy()
	c.Translator.TranslateTo(hostConfigMap)

	return hostConfigMap
}
