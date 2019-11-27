package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"knative.dev/serving/pkg/apis/serving"
)

type scopedPodCounter struct {
	podsLister  corev1listers.PodLister
	namespace   string
	serviceName string
}

type NotReadyPodCounter interface {
	NotReadyCount() (int, int, error)
}

func NewScopedPodsCounter(lister corev1listers.PodLister, namespace, serviceName string) NotReadyPodCounter {
	return &scopedPodCounter{
		podsLister:  lister,
		namespace:   namespace,
		serviceName: serviceName,
	}
}

func (pc *scopedPodCounter) NotReadyCount() (int, int, error) {
	filterLabels := []map[string]string{
		{serving.ServiceLabelKey: pc.serviceName},
	}

	var pending, terminating int
	for _, l := range filterLabels {
		pods, err := pc.podsLister.List(labels.Set(l).AsSelector())
		if err != nil {
			return 0, 0, err
		}
		pending, terminating = pendingTerminatingCount(pods)
	}

	return pending, terminating, nil
}

func pendingTerminatingCount(pods []*corev1.Pod) (int, int) {
	pending, terminating := 0, 0
	for _, pod := range pods {
		if pod.ObjectMeta.DeletionTimestamp != nil && pod.Status.Phase == corev1.PodRunning {
			terminating++
			continue
		}
		if pod.Status.Phase == corev1.PodPending {
			pending++
		}
	}
	return pending, terminating
}
