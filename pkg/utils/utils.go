package utils

import (
	"fmt"
	machinev1 "github.com/openshift/api/machine/v1beta1"
)

func GetClusterNameWithNamespace(machine *machinev1.Machine) string {
	clusterName := machine.Labels[machinev1.MachineClusterIDLabel]
	return fmt.Sprintf("%s-%s", machine.Namespace, clusterName)
}
