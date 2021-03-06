package services

import (
	"fmt"
	"strconv"
	"strings"

	kubeapi "k8s.io/client-go/pkg/api"
	kubev1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/batch"
	metav1 "k8s.io/client-go/pkg/apis/meta/v1"

	"kubevirt.io/kubevirt/pkg/api/v1"
	"kubevirt.io/kubevirt/pkg/logging"
	"kubevirt.io/kubevirt/pkg/precond"
)

type TemplateService interface {
	RenderLaunchManifest(*v1.VM) (*kubev1.Pod, error)
	RenderMigrationJob(*v1.VM, *kubev1.Node, *kubev1.Node) (*batch.Job, error)
}

type templateService struct {
	launcherImage string
}

//Deprecated: remove the service and just use a builder or contextcless helper function
func (t *templateService) RenderLaunchManifest(vm *v1.VM) (*kubev1.Pod, error) {
	precond.MustNotBeNil(vm)
	domain := precond.MustNotBeEmpty(vm.GetObjectMeta().GetName())
	uid := precond.MustNotBeEmpty(string(vm.GetObjectMeta().GetUID()))

	// VM target container
	container := kubev1.Container{
		Name:            "compute",
		Image:           t.launcherImage,
		ImagePullPolicy: kubev1.PullIfNotPresent,
		Command:         []string{"/virt-launcher", "-qemu-timeout", "60s"},
	}

	// Set up spice ports
	ports := []kubev1.ContainerPort{}
	for i, g := range vm.Spec.Domain.Devices.Graphics {
		if strings.ToLower(g.Type) == "spice" {
			ports = append(ports, kubev1.ContainerPort{
				ContainerPort: g.Port,
				Name:          "spice" + strconv.Itoa(i),
			})
		}
	}
	container.Ports = ports

	// TODO use constants for labels
	pod := kubev1.Pod{
		ObjectMeta: kubev1.ObjectMeta{
			GenerateName: "virt-launcher-" + domain + "-----",
			Labels: map[string]string{
				v1.AppLabel:    "virt-launcher",
				v1.DomainLabel: domain,
				v1.UIDLabel:    uid,
			},
		},
		Spec: kubev1.PodSpec{
			RestartPolicy: kubev1.RestartPolicyNever,
			Containers:    []kubev1.Container{container},
			NodeSelector:  vm.Spec.NodeSelector,
		},
	}

	return &pod, nil
}

func (t *templateService) RenderMigrationJob(vm *v1.VM, sourceNode *kubev1.Node, targetNode *kubev1.Node) (*batch.Job, error) {
	srcAddr := ""
	dstAddr := ""
	for _, addr := range sourceNode.Status.Addresses {
		if addr.Type == kubev1.NodeHostName {
			srcAddr = addr.Address
			break
		}
		if (addr.Type == kubev1.NodeInternalIP) && (srcAddr == "") {
			// record this address, but keep iterating addresses. A NodeHostName record
			// would be preferred if present.
			srcAddr = addr.Address
		}
	}
	if srcAddr == "" {
		err := fmt.Errorf("migration source node is unreachable")
		logging.DefaultLogger().Error().Msg("migration target node is unreachable")
		return nil, err
	}
	srcUri := fmt.Sprintf("qemu+tcp://%s", srcAddr)

	for _, addr := range targetNode.Status.Addresses {
		if addr.Type == kubev1.NodeHostName {
			dstAddr = addr.Address
			break
		}
		if (addr.Type == kubev1.NodeInternalIP) && (dstAddr == "") {
			dstAddr = addr.Address
		}
	}
	if dstAddr == "" {
		err := fmt.Errorf("migration target node is unreachable")
		logging.DefaultLogger().Error().Msg("migration target node is unreachable")
		return nil, err
	}
	destUri := fmt.Sprintf("qemu+tcp://%s", dstAddr)

	job := batch.Job{
		ObjectMeta: kubeapi.ObjectMeta{
			GenerateName: "virt-migration",
		},
		TypeMeta: metav1.TypeMeta{
			Kind: "Job",
		},
		Spec: batch.JobSpec{
			Template: kubeapi.PodTemplateSpec{
				Spec: kubeapi.PodSpec{
					RestartPolicy: kubeapi.RestartPolicyNever,
					Containers: []kubeapi.Container{
						kubeapi.Container{
							Name:  "virt-migration",
							Image: "kubevirt/virt-handler:devel",
							Command: []string{
								"virsh", "migrate", vm.Spec.Domain.Name, destUri, srcUri,
							},
						},
					},
				},
			},
		},
	}

	return &job, nil
}

func NewTemplateService(launcherImage string) (TemplateService, error) {
	precond.MustNotBeEmpty(launcherImage)
	svc := templateService{
		launcherImage: launcherImage,
	}
	return &svc, nil
}
