// Package syncer mirrors the resources a virtual cluster's pods depend on -
// configmaps, secrets, services, ingresses, persistent volume claims, priority
// classes and events - between the virtual cluster and the host cluster.
package syncer

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

// Context holds the clients and the translator the syncers use to move resources
// between the virtual and the host cluster.
type Context struct {
	ClusterName      string
	ClusterNamespace string
	VirtualClient    client.Client
	HostClient       client.Client
	Translator       translate.ToHostTranslator
}

// filterResource determines whether a given Kubernetes object should be included based on the provided
// enabled flag, label selector, and match expressions.
//
// It returns true if the object matches the criteria or if the selector is empty, and false otherwise.
// If the resource is not enabled, it only returns true if the object has a deletion timestamp.
func filterResource(object client.Object, enabled bool, selector map[string]string, matchExpressions []metav1.LabelSelectorRequirement) bool {
	if !enabled {
		return object.GetDeletionTimestamp() != nil
	}

	labelSelector := &metav1.LabelSelector{
		MatchLabels:      selector,
		MatchExpressions: matchExpressions,
	}

	parsedSelector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return false
	}

	return parsedSelector.Empty() || parsedSelector.Matches(labels.Set(object.GetLabels()))
}
