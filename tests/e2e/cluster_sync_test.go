package e2e_test

import (
	"context"
	"time"

	"github.com/onsi/gomega/gcustom"
	"github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	fwk3k "github.com/rancher/k3k/tests/framework/k3k"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = When("a shared mode cluster is created", Ordered, Label(syncTestsLabel), func() {
	var (
		virtualCluster   *VirtualCluster
		virtualConfigMap *corev1.ConfigMap
		virtualService   *corev1.Service
	)

	BeforeAll(func() {
		virtualCluster = NewVirtualCluster()

		DeferCleanup(func() {
			fwk3k.DeleteNamespaces(k8s, virtualCluster.Cluster.Namespace)
		})
	})

	When("a ConfigMap is created in the virtual cluster", func() {
		BeforeAll(func(ctx context.Context) {
			virtualConfigMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cm",
					Namespace: "default",
				},
			}

			var err error
			// TODO: Create a configmap with the selector.
			virtualConfigMap, err = virtualCluster.Client.CoreV1().ConfigMaps("default").Create(ctx, virtualConfigMap, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("is replicated in the host cluster", func(ctx context.Context) {
			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualConfigMap)

			// check that the ConfigMap is synced in the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().ConfigMaps(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})

	When("the Cluster ConfigMap sync config is set", func() {
		var virtualConfigMap1, virtualConfigMap2 *corev1.ConfigMap

		BeforeAll(func(ctx context.Context) {
			// Reload the cluster
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(virtualCluster.Cluster), virtualCluster.Cluster)
			Expect(err).To(Not(HaveOccurred()))

			virtualCluster.Cluster.Spec.Sync = &v1beta1.SyncConfig{
				ConfigMaps: v1beta1.ConfigMapSyncConfig{
					Enabled: true,
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "environment",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{"production", "staging"},
						},
					},
				},
			}
			err = k8sClient.Update(ctx, virtualCluster.Cluster)
			Expect(err).To(Not(HaveOccurred()))

			// Create two additional ConfigMaps with the labels.
			virtualConfigMap1 = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cm-1",
					Namespace: "default",
					Labels: map[string]string{
						"environment": "production",
					},
				},
			}

			virtualConfigMap1, err = virtualCluster.Client.CoreV1().ConfigMaps("default").Create(ctx, virtualConfigMap1, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			virtualConfigMap2 = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cm-2",
					Namespace: "default",
					Labels: map[string]string{
						"environment": "dev",
					},
				},
			}

			virtualConfigMap2, err = virtualCluster.Client.CoreV1().ConfigMaps("default").Create(ctx, virtualConfigMap2, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("one of them is replicated in the host cluster", func(ctx context.Context) {
			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			translatedConfigMap1 := hostTranslator.NamespacedName(virtualConfigMap1)
			translatedConfigMap2 := hostTranslator.NamespacedName(virtualConfigMap2)

			Eventually(func(g Gomega) {
				// check that the virtualConfigMap1 is synced in the host cluster because it has the matching label.
				_, err := k8s.CoreV1().ConfigMaps(translatedConfigMap1.Namespace).Get(ctx, translatedConfigMap1.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())

			// This is done in two tests because this test can succeed because
			//  we can't tell whether or not it doesn't exist because it hasn't been synced or not.
			Eventually(func(g Gomega) {
				// check that the virtualConfigMap2 is not synced in the host cluster because it does not have the matching label.
				_, err := k8s.CoreV1().ConfigMaps(translatedConfigMap2.Namespace).Get(ctx, translatedConfigMap2.Name, metav1.GetOptions{})
				g.Expect(err).To(BeNotFound())
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})

	When("a Service is created in the virtual cluster", func() {
		BeforeAll(func(ctx context.Context) {
			virtualService = &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-svc",
					Namespace: "default",
				},
				Spec: corev1.ServiceSpec{
					Type:  corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{{Port: 8888}},
				},
			}

			var err error

			virtualService, err = virtualCluster.Client.CoreV1().Services("default").Create(ctx, virtualService, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
		})

		It("is replicated in the host cluster", func(ctx context.Context) {
			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualService)

			// check that the Service is synced in the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().Services(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(err).To(Not(HaveOccurred()))
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})

	When("the cluster is deleted", func() {
		BeforeAll(func(ctx context.Context) {
			By("Deleting cluster")

			err := k8sClient.Delete(ctx, virtualCluster.Cluster)
			Expect(err).To(Not(HaveOccurred()))
		})

		It("will delete the ConfigMap from the host cluster", func(ctx context.Context) {
			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualConfigMap)

			// check that the ConfigMap is deleted from the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().ConfigMaps(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})

		It("will delete the Service from the host cluster", func(ctx context.Context) {
			hostTranslator := translate.NewHostTranslator(virtualCluster.Cluster)
			namespacedName := hostTranslator.NamespacedName(virtualService)

			// check that the Service is deleted from the host cluster
			Eventually(func(g Gomega) {
				_, err := k8s.CoreV1().Services(namespacedName.Namespace).Get(ctx, namespacedName.Name, metav1.GetOptions{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).
				WithTimeout(time.Minute).
				WithPolling(time.Second).
				Should(Succeed())
		})
	})
})

func BeNotFound() types.GomegaMatcher {
	return gcustom.MakeMatcher(func(actual error) (bool, error) {
		return apierrors.IsNotFound(actual), nil
	}).WithTemplate("Expected\n\t{{.Actual}} to be not found")
}
