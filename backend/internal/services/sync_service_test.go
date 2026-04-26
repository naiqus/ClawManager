package services

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestMapStatefulSetToInstanceStatus(t *testing.T) {
	zero := int32(0)
	one := int32(1)

	tests := []struct {
		name     string
		sts      *appsv1.StatefulSet
		pod      *corev1.Pod
		previous string
		want     string
	}{
		{
			name:     "nil sts when previous=running",
			sts:      nil,
			previous: "running",
			want:     "stopped",
		},
		{
			name:     "nil sts when previous=creating",
			sts:      nil,
			previous: "creating",
			want:     "error",
		},
		{
			name: "scaled to 0",
			sts:  &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: &zero}},
			want: "stopped",
		},
		{
			name: "ready replica with pod ready",
			sts: &appsv1.StatefulSet{
				Spec:   appsv1.StatefulSetSpec{Replicas: &one},
				Status: appsv1.StatefulSetStatus{ReadyReplicas: 1},
			},
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			},
			want: "running",
		},
		{
			name: "pod failed",
			sts:  &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: &one}},
			pod:  &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}},
			want: "error",
		},
		{
			name: "replicas=1 but pod not ready yet",
			sts: &appsv1.StatefulSet{
				Spec:   appsv1.StatefulSetSpec{Replicas: &one},
				Status: appsv1.StatefulSetStatus{ReadyReplicas: 0},
			},
			pod:  &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}},
			want: "creating",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapStatefulSetToInstanceStatus(tc.sts, tc.pod, tc.previous)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
