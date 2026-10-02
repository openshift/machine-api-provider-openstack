package utils

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"

	machinev1 "github.com/openshift/api/machine/v1beta1"
)

func GetClusterNameWithNamespace(machine *machinev1.Machine) string {
	clusterName := machine.Labels[machinev1.MachineClusterIDLabel]
	return fmt.Sprintf("%s-%s", machine.Namespace, clusterName)
}

func Eventf(recorder events.EventRecorder, object runtime.Object, typ, reason, message string, args ...interface{}) {
	recorder.Eventf(object, nil, typ, reason, reason, message, args...)
}
