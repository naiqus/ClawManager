package services

import (
	"context"
	"fmt"
	"time"

	"clawreef/internal/models"
	"clawreef/internal/repository"
	"clawreef/internal/services/k8s"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// SyncService handles synchronization between database and K8s state
type SyncService struct {
	instanceRepo         repository.InstanceRepository
	runtimeStatusService InstanceRuntimeStatusService
	podService           *k8s.PodService
	stsService           *k8s.StatefulSetService
	interval             time.Duration
	stopChan             chan struct{}
}

// NewSyncService creates a new sync service
func NewSyncService(instanceRepo repository.InstanceRepository, runtimeStatusService InstanceRuntimeStatusService) *SyncService {
	return &SyncService{
		instanceRepo:         instanceRepo,
		runtimeStatusService: runtimeStatusService,
		podService:           k8s.NewPodService(),
		stsService:           k8s.NewStatefulSetService(),
		interval:             5 * time.Second, // Sync every 5 seconds for more responsive status updates
		stopChan:             make(chan struct{}),
	}
}

// Start starts the sync service
func (s *SyncService) Start() {
	fmt.Println("Starting K8s state sync service...")
	go s.syncLoop()
}

// Stop stops the sync service
func (s *SyncService) Stop() {
	close(s.stopChan)
}

// syncLoop runs the synchronization loop
func (s *SyncService) syncLoop() {
	fmt.Printf("[SyncService] Starting sync loop with interval %v\n", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run immediately on start
	fmt.Println("[SyncService] Running initial sync...")
	s.syncAllInstances()
	fmt.Println("[SyncService] Initial sync complete")

	for {
		select {
		case <-ticker.C:
			fmt.Println("[SyncService] Tick - running scheduled sync...")
			s.syncAllInstances()
		case <-s.stopChan:
			fmt.Println("[SyncService] Stopping K8s state sync service...")
			return
		}
	}
}

// syncAllInstances synchronizes the state of all instances
func (s *SyncService) syncAllInstances() {
	ctx := context.Background()

	fmt.Println("[SyncService] Fetching all instances from database...")
	// Get all running instances from database
	instances, err := s.instanceRepo.GetAllRunning()
	if err != nil {
		fmt.Printf("[SyncService] Error getting running instances: %v\n", err)
		return
	}

	fmt.Printf("[SyncService] Found %d instances to sync\n", len(instances))

	if len(instances) == 0 {
		fmt.Println("[SyncService] No instances found, skipping sync")
		return
	}

	for i, instance := range instances {
		fmt.Printf("[SyncService] Syncing instance %d/%d: ID=%d, Status=%s\n",
			i+1, len(instances), instance.ID, instance.Status)
		s.syncInstance(ctx, &instance)
	}

	fmt.Println("[SyncService] Sync complete")
}

// syncInstance synchronizes a single instance's state
func (s *SyncService) syncInstance(ctx context.Context, instance *models.Instance) {
	sts, stsErr := s.stsService.GetStatefulSet(ctx, instance.UserID, instance.ID)
	var pod *corev1.Pod
	if stsErr == nil {
		pod, _ = s.stsService.GetPod(ctx, instance.UserID, instance.ID)
	}

	desiredStatus := mapStatefulSetToInstanceStatus(sts, pod, instance.Status)

	needsUpdate := false
	if instance.Status != desiredStatus {
		fmt.Printf("Instance %d: STS status maps to %s, was %s\n", instance.ID, desiredStatus, instance.Status)
		instance.Status = desiredStatus
		needsUpdate = true
	}
	s.updateInfraStatus(instance.ID, desiredStatus)

	if pod != nil {
		if pod.Status.PodIP != "" && (instance.PodIP == nil || *instance.PodIP != pod.Status.PodIP) {
			ip := pod.Status.PodIP
			instance.PodIP = &ip
			needsUpdate = true
		}
		if instance.PodName == nil || *instance.PodName != pod.Name {
			pn := pod.Name
			instance.PodName = &pn
			needsUpdate = true
		}
		if instance.PodNamespace == nil || *instance.PodNamespace != pod.Namespace {
			pns := pod.Namespace
			instance.PodNamespace = &pns
			needsUpdate = true
		}
	} else if desiredStatus == "stopped" {
		// Clear PodIP only; keep PodName/Namespace so Start is fast.
		if instance.PodIP != nil {
			instance.PodIP = nil
			needsUpdate = true
		}
	}

	if needsUpdate {
		instance.UpdatedAt = time.Now()
		if err := s.instanceRepo.Update(instance); err != nil {
			fmt.Printf("Error updating instance %d: %v\n", instance.ID, err)
		} else {
			GetHub().BroadcastInstanceStatus(instance.UserID, instance)
		}
	}
}

// mapStatefulSetToInstanceStatus translates STS+pod state to the instance
// status enum used by the API and DB.
//
//	previous is the instance.Status value at entry; used only to decide
//	whether a missing STS should be reported as "stopped" or "error" (the
//	latter when the instance was still in the "creating" phase, mirroring
//	the prior bare-pod behavior).
func mapStatefulSetToInstanceStatus(sts *appsv1.StatefulSet, pod *corev1.Pod, previous string) string {
	if sts == nil {
		if previous == "creating" {
			return "error"
		}
		return "stopped"
	}
	if sts.Spec.Replicas != nil && *sts.Spec.Replicas == 0 {
		return "stopped"
	}
	if pod != nil && pod.Status.Phase == corev1.PodFailed {
		return "error"
	}
	if sts.Status.ReadyReplicas == 1 && pod != nil && isPodReady(pod) {
		return "running"
	}
	return "creating"
}

func (s *SyncService) updateInfraStatus(instanceID int, instanceStatus string) {
	if s.runtimeStatusService == nil {
		return
	}
	infraStatus := mapInstanceStatusToInfraStatus(instanceStatus)
	if err := s.runtimeStatusService.UpsertInfraStatus(instanceID, infraStatus); err != nil {
		fmt.Printf("Error updating runtime infra status for instance %d: %v\n", instanceID, err)
	}
}

func mapInstanceStatusToInfraStatus(instanceStatus string) string {
	switch instanceStatus {
	case "running":
		return "ready"
	case "stopped":
		return "stopped"
	case "error":
		return "error"
	case "creating":
		return "creating"
	default:
		return "creating"
	}
}

func mapPodToInstanceStatus(pod *corev1.Pod) string {
	if pod == nil {
		return "error"
	}

	switch pod.Status.Phase {
	case corev1.PodRunning:
		if isPodReady(pod) {
			return "running"
		}
		return "creating"
	case corev1.PodPending:
		return "creating"
	case corev1.PodSucceeded:
		return "stopped"
	case corev1.PodFailed, corev1.PodUnknown:
		return "error"
	default:
		return "creating"
	}
}

func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// ForceSync forces an immediate sync of all instances
func (s *SyncService) ForceSync() {
	s.syncAllInstances()
}
