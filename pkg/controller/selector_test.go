package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_FilterResource(t *testing.T) {
	now := metav1.Now()

	storageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "fast",
			Labels: map[string]string{"environment": "production"},
		},
	}

	deletedStorageClass := storageClass.DeepCopy()
	deletedStorageClass.DeletionTimestamp = &now

	tests := map[string]struct {
		enabled          bool
		selector         map[string]string
		matchExpressions []metav1.LabelSelectorRequirement
		object           *storagev1.StorageClass
		expected         bool
	}{
		"disabled": {
			object:   storageClass,
			expected: false,
		},
		"disabled with deletion": {
			object:   deletedStorageClass,
			expected: true,
		},
		"enabled with empty selector": {
			enabled:  true,
			object:   storageClass,
			expected: true,
		},
		"enabled with matching selector": {
			enabled:  true,
			selector: map[string]string{"environment": "production"},
			object:   storageClass,
			expected: true,
		},
		"enabled with non-matching selector": {
			enabled:  true,
			selector: map[string]string{"environment": "staging"},
			object:   storageClass,
			expected: false,
		},
		"enabled with matching expression": {
			enabled: true,
			matchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "environment", Operator: metav1.LabelSelectorOpIn, Values: []string{"production", "staging"}},
			},
			object:   storageClass,
			expected: true,
		},
		"enabled with non-matching expression": {
			enabled: true,
			matchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "environment", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"production"}},
			},
			object:   storageClass,
			expected: false,
		},
		"enabled with matching selector and non-matching expression": {
			enabled:  true,
			selector: map[string]string{"environment": "production"},
			matchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "tier", Operator: metav1.LabelSelectorOpExists},
			},
			object:   storageClass,
			expected: false,
		},
		"invalid expression is ignored": {
			enabled: true,
			matchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "environment", Operator: metav1.LabelSelectorOpIn},
			},
			object:   storageClass,
			expected: true,
		},
		"invalid expression is ignored and valid expression applied": {
			enabled: true,
			matchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "environment", Operator: "Unknown", Values: []string{"production"}},
				{Key: "environment", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"production"}},
			},
			object:   storageClass,
			expected: false,
		},
		"invalid selector is ignored and valid selector applied": {
			enabled:  true,
			selector: map[string]string{"invalid key!": "value", "environment": "staging"},
			object:   storageClass,
			expected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := FilterResource(context.Background(), tt.object, tt.enabled, tt.selector, tt.matchExpressions)
			assert.Equal(t, tt.expected, got)
		})
	}
}
