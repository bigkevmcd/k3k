package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// FilterResource determines whether a given Kubernetes object should be included based on the provided
// enabled flag, label selector, and match expressions.
//
// It returns true if the object matches the criteria or if the selector is empty, and false otherwise.
// If the resource is not enabled, it only returns true if the object has a deletion timestamp.
//
// Invalid labels or expressions are logged and ignored, the remaining requirements are still applied.
func FilterResource(ctx context.Context, object client.Object, enabled bool, selector map[string]string, matchExpressions []metav1.LabelSelectorRequirement) bool {
	if !enabled {
		return object.GetDeletionTimestamp() != nil
	}

	parsedSelector := buildSelector(ctx, selector, matchExpressions)

	return parsedSelector.Empty() || parsedSelector.Matches(labels.Set(object.GetLabels()))
}

// buildSelector converts the labels and expressions into a selector, skipping and logging any that are invalid.
func buildSelector(ctx context.Context, selector map[string]string, matchExpressions []metav1.LabelSelectorRequirement) labels.Selector {
	log := ctrl.LoggerFrom(ctx)
	result := labels.NewSelector()

	for key, value := range selector {
		requirements, err := parseRequirements(&metav1.LabelSelector{MatchLabels: map[string]string{key: value}})
		if err != nil {
			log.Error(err, "ignoring invalid label selector", "key", key, "value", value)
			continue
		}

		result = result.Add(requirements...)
	}

	for _, expression := range matchExpressions {
		requirements, err := parseRequirements(&metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{expression}})
		if err != nil {
			log.Error(err, "ignoring invalid match expression", "key", expression.Key, "operator", expression.Operator, "values", expression.Values)
			continue
		}

		result = result.Add(requirements...)
	}

	return result
}

func parseRequirements(labelSelector *metav1.LabelSelector) (labels.Requirements, error) {
	parsed, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return nil, err
	}

	requirements, _ := parsed.Requirements()

	return requirements, nil
}
