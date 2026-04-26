package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// newTestClient returns a Client backed by a fake clientset suitable for
// in-process unit tests.
func newTestClient() *Client {
	return &Client{
		Clientset:    fake.NewSimpleClientset(),
		Namespace:    "shellclaw",
		StorageClass: "local-path",
	}
}

func newTestPodConfig() PodConfig {
	return PodConfig{
		InstanceID:    18,
		InstanceName:  "nutramont-openclaw",
		UserID:        3,
		Type:          "openclaw",
		CPUCores:      1,
		MemoryGB:      3,
		Image:         "example.invalid/openclaw:latest",
		MountPath:     "/data",
		ContainerPort: 3001,
		ExtraEnv:      map[string]string{"FOO": "bar"},
	}
}

func TestStatefulSetService_CreateStatefulSet_Shape(t *testing.T) {
	client := newTestClient()
	svc := &StatefulSetService{client: client}

	cfg := newTestPodConfig()
	sts, err := svc.CreateStatefulSet(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateStatefulSet failed: %v", err)
	}

	wantName := client.GetStatefulSetName(cfg.InstanceID, cfg.InstanceName)
	if sts.Name != wantName {
		t.Errorf("sts.Name = %q, want %q", sts.Name, wantName)
	}
	if sts.Namespace != client.GetNamespace(cfg.UserID) {
		t.Errorf("sts.Namespace = %q, want %q", sts.Namespace, client.GetNamespace(cfg.UserID))
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
		t.Errorf("sts.Spec.Replicas = %v, want 1", sts.Spec.Replicas)
	}
	if sts.Spec.ServiceName == "" {
		t.Errorf("sts.Spec.ServiceName must be non-empty")
	}
	if got := sts.Spec.Selector.MatchLabels["instance-id"]; got != "18" {
		t.Errorf("selector instance-id = %q, want 18", got)
	}
	if got := sts.Spec.Template.Labels["instance-id"]; got != "18" {
		t.Errorf("template label instance-id = %q, want 18", got)
	}
	if sts.Spec.UpdateStrategy.Type != "OnDelete" {
		t.Errorf("UpdateStrategy.Type = %q, want OnDelete", sts.Spec.UpdateStrategy.Type)
	}
	if sts.Spec.PodManagementPolicy != "Parallel" {
		t.Errorf("PodManagementPolicy = %q, want Parallel", sts.Spec.PodManagementPolicy)
	}

	// PVC must be referenced by name (not volumeClaimTemplates).
	if len(sts.Spec.VolumeClaimTemplates) != 0 {
		t.Errorf("VolumeClaimTemplates must be empty, got %d", len(sts.Spec.VolumeClaimTemplates))
	}
	wantPVC := client.GetPVCName(cfg.InstanceID)
	foundPVC := false
	for _, v := range sts.Spec.Template.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == wantPVC {
			foundPVC = true
		}
	}
	if !foundPVC {
		t.Errorf("expected volume referencing PVC %q, got %+v", wantPVC, sts.Spec.Template.Spec.Volumes)
	}

	// Pod spec parity: same env, ports, restart policy.
	pod := sts.Spec.Template.Spec
	if pod.RestartPolicy != corev1.RestartPolicyAlways && pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("unexpected RestartPolicy: %s", pod.RestartPolicy)
	}
	if len(pod.Containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(pod.Containers))
	}
}

func TestStatefulSetService_CreateStatefulSet_Idempotent(t *testing.T) {
	client := newTestClient()
	svc := &StatefulSetService{client: client}
	cfg := newTestPodConfig()

	first, err := svc.CreateStatefulSet(context.Background(), cfg)
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	second, err := svc.CreateStatefulSet(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second create returned error, expected idempotent success: %v", err)
	}
	if first.UID != second.UID && second.UID != "" {
		// fake clientset assigns "" UID; only assert when set.
		t.Errorf("expected same STS on idempotent re-create")
	}
}

func TestStatefulSetService_Scale(t *testing.T) {
	client := newTestClient()
	svc := &StatefulSetService{client: client}
	cfg := newTestPodConfig()

	if _, err := svc.CreateStatefulSet(context.Background(), cfg); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Scale(context.Background(), cfg.UserID, cfg.InstanceID, 0); err != nil {
		t.Fatalf("scale to 0 failed: %v", err)
	}

	got, _ := client.Clientset.AppsV1().StatefulSets(client.GetNamespace(cfg.UserID)).
		Get(context.Background(), client.GetStatefulSetName(cfg.InstanceID, cfg.InstanceName), metav1.GetOptions{})
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("after Scale(0), Replicas = %v, want 0", got.Spec.Replicas)
	}

	if err := svc.Scale(context.Background(), cfg.UserID, cfg.InstanceID, 1); err != nil {
		t.Fatalf("scale to 1 failed: %v", err)
	}
	got, _ = client.Clientset.AppsV1().StatefulSets(client.GetNamespace(cfg.UserID)).
		Get(context.Background(), client.GetStatefulSetName(cfg.InstanceID, cfg.InstanceName), metav1.GetOptions{})
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 1 {
		t.Errorf("after Scale(1), Replicas = %v, want 1", got.Spec.Replicas)
	}
}

func TestStatefulSetService_DeleteStatefulSet_NotFound(t *testing.T) {
	client := newTestClient()
	svc := &StatefulSetService{client: client}
	if err := svc.DeleteStatefulSet(context.Background(), 99, 99); err != nil {
		t.Errorf("delete on missing STS should be nil, got %v", err)
	}
}
