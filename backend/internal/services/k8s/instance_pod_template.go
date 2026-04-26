package k8s

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// instancePodLabels returns the canonical pod labels for an instance.
// Used by both PodService (legacy bare pods) and StatefulSetService (new path).
func instancePodLabels(cfg PodConfig) map[string]string {
	return map[string]string{
		"app":           "clawreef",
		"instance-id":   fmt.Sprintf("%d", cfg.InstanceID),
		"instance-name": cfg.InstanceName,
		"user-id":       fmt.Sprintf("%d", cfg.UserID),
		"instance-type": cfg.Type,
		"managed-by":    "clawreef",
	}
}

// buildInstancePodSpec returns the canonical PodSpec for an instance.
// Callers wrap it in either a corev1.Pod or appsv1.StatefulSet.spec.template.
// The PVC is referenced by name (not via volumeClaimTemplates) so the
// existing manually-managed PVC lifecycle keeps working.
func buildInstancePodSpec(cfg PodConfig, pvcName string) corev1.PodSpec {
	if cfg.ContainerPort == 0 {
		cfg.ContainerPort = 3001
	}

	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%g", cfg.CPUCores)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", cfg.MemoryGB)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%g", cfg.CPUCores)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", cfg.MemoryGB)),
		},
	}
	if cfg.GPUEnabled && cfg.GPUCount > 0 {
		resources.Limits["nvidia.com/gpu"] = resource.MustParse(fmt.Sprintf("%d", cfg.GPUCount))
		resources.Requests["nvidia.com/gpu"] = resource.MustParse(fmt.Sprintf("%d", cfg.GPUCount))
	}

	container := corev1.Container{
		Name:  "desktop",
		Image: cfg.Image,
		Ports: []corev1.ContainerPort{
			{ContainerPort: cfg.ContainerPort, Name: "http"},
		},
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(cfg.ContainerPort)},
			},
			FailureThreshold: 30,
			PeriodSeconds:    5,
			TimeoutSeconds:   2,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(cfg.ContainerPort)},
			},
			InitialDelaySeconds: 3,
			PeriodSeconds:       5,
			TimeoutSeconds:      2,
			FailureThreshold:    6,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(cfg.ContainerPort)},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       10,
			TimeoutSeconds:      2,
			FailureThreshold:    3,
		},
		Resources: resources,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "data", MountPath: cfg.MountPath},
		},
		Env: []corev1.EnvVar{
			{Name: "INSTANCE_ID", Value: fmt.Sprintf("%d", cfg.InstanceID)},
			{Name: "USER_ID", Value: fmt.Sprintf("%d", cfg.UserID)},
		},
	}
	for key, value := range cfg.ExtraEnv {
		container.Env = append(container.Env, corev1.EnvVar{Name: key, Value: value})
	}
	for _, secretName := range cfg.EnvFromSecretNames {
		if secretName == "" {
			continue
		}
		container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			},
		})
	}

	return corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers:    []corev1.Container{container},
		Volumes: []corev1.Volume{
			{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
					},
				},
			},
		},
	}
}

// buildInstancePodObjectMeta returns the canonical ObjectMeta for an instance pod.
func buildInstancePodObjectMeta(cfg PodConfig, name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    instancePodLabels(cfg),
	}
}
