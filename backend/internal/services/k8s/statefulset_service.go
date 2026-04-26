package k8s

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatefulSetService manages the apps/v1.StatefulSet wrapping each
// ClawReef instance pod. Replaces the bare-Pod path so K8s itself
// recreates the pod after node-controller eviction.
type StatefulSetService struct {
	client *Client
}

// NewStatefulSetService creates a new StatefulSet service bound to the
// global K8s client. Returns nil if the client is not initialized; callers
// that may run before Initialize() must nil-check.
func NewStatefulSetService() *StatefulSetService {
	return &StatefulSetService{client: globalClient}
}

// ErrStatefulSetNotFound is returned by Scale when the underlying STS does
// not exist. instance_service.Start uses this to fall through to
// CreateStatefulSet for cold-start / post-delete recovery.
var ErrStatefulSetNotFound = errors.New("statefulset not found")

// CreateStatefulSet creates a single-replica StatefulSet for an instance.
// Idempotent: if the STS already exists and is not being deleted, returns
// the existing object. Callers needing to start a stopped STS must call
// Scale(1) explicitly.
func (s *StatefulSetService) CreateStatefulSet(ctx context.Context, cfg PodConfig) (*appsv1.StatefulSet, error) {
	if s.client == nil {
		return nil, fmt.Errorf("k8s client not initialized")
	}

	stsName := s.client.GetStatefulSetName(cfg.InstanceID, cfg.InstanceName)
	namespace := s.client.GetNamespace(cfg.UserID)
	pvcName := s.client.GetPVCName(cfg.InstanceID)
	serviceName := s.client.GetServiceName(cfg.InstanceID, cfg.InstanceName)

	one := int32(1)
	labels := instancePodLabels(cfg)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      stsName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:            &one,
			ServiceName:         serviceName,
			PodManagementPolicy: appsv1.ParallelPodManagement,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.OnDeleteStatefulSetStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"instance-id": fmt.Sprintf("%d", cfg.InstanceID),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       buildInstancePodSpec(cfg, pvcName),
			},
		},
	}

	created, err := s.client.Clientset.AppsV1().StatefulSets(namespace).Create(ctx, sts, metav1.CreateOptions{})
	if err == nil {
		return created, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create statefulset %s: %w", stsName, err)
	}
	// Already exists — return the existing object as success (idempotent).
	existing, getErr := s.client.Clientset.AppsV1().StatefulSets(namespace).Get(ctx, stsName, metav1.GetOptions{})
	if getErr != nil {
		return nil, fmt.Errorf("statefulset %s exists but get failed: %w", stsName, getErr)
	}
	return existing, nil
}

// GetStatefulSet returns the STS for an instance, or NotFound.
func (s *StatefulSetService) GetStatefulSet(ctx context.Context, userID, instanceID int) (*appsv1.StatefulSet, error) {
	if s.client == nil {
		return nil, fmt.Errorf("k8s client not initialized")
	}
	namespace := s.client.GetNamespace(userID)
	list, err := s.client.Clientset.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instance-id=%d", instanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list statefulsets: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, ErrStatefulSetNotFound
	}
	return &list.Items[0], nil
}

// GetPod returns the "<sts>-0" pod for an instance, or NotFound. Used by
// SyncService to read pod-level fields (IP, ready condition).
func (s *StatefulSetService) GetPod(ctx context.Context, userID, instanceID int) (*corev1.Pod, error) {
	if s.client == nil {
		return nil, fmt.Errorf("k8s client not initialized")
	}
	namespace := s.client.GetNamespace(userID)
	pods, err := s.client.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instance-id=%d", instanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("pod not found for instance %d", instanceID)
	}
	return &pods.Items[0], nil
}

// Scale sets the STS replica count. Returns ErrStatefulSetNotFound if the
// STS is missing — callers can fall back to CreateStatefulSet.
func (s *StatefulSetService) Scale(ctx context.Context, userID, instanceID int, replicas int32) error {
	if s.client == nil {
		return fmt.Errorf("k8s client not initialized")
	}
	sts, err := s.GetStatefulSet(ctx, userID, instanceID)
	if err != nil {
		return err
	}
	sts.Spec.Replicas = &replicas
	_, err = s.client.Clientset.AppsV1().StatefulSets(sts.Namespace).Update(ctx, sts, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale statefulset %s: %w", sts.Name, err)
	}
	return nil
}

// DeleteStatefulSet deletes the STS with foreground cascade so the pod is
// fully terminated before the call returns. NotFound is treated as success.
func (s *StatefulSetService) DeleteStatefulSet(ctx context.Context, userID, instanceID int) error {
	if s.client == nil {
		return fmt.Errorf("k8s client not initialized")
	}
	sts, err := s.GetStatefulSet(ctx, userID, instanceID)
	if err != nil {
		if errors.Is(err, ErrStatefulSetNotFound) {
			return nil
		}
		return err
	}
	fg := metav1.DeletePropagationForeground
	err = s.client.Clientset.AppsV1().StatefulSets(sts.Namespace).Delete(ctx, sts.Name, metav1.DeleteOptions{
		PropagationPolicy: &fg,
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete statefulset %s: %w", sts.Name, err)
	}
	return nil
}
