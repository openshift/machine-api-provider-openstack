#!/usr/bin/python3

import json
import subprocess
import sys

namespace = sys.argv[1]
name = sys.argv[2]

# Fetch current overrides
while True:
    proc = subprocess.run("oc get clusterversion/version -o json", shell=True,
                                                                   capture_output=True)

    clusterversion = json.loads(proc.stdout)
    annotations = clusterversion.get('metadata', {}).get('annotations', {})
    if 'kubectl.kubernetes.io/last-applied-configuration' in annotations:
        break

    # Ensure clusterversion/version is annotated with last-applied-configuration
    subprocess.run(["oc", "apply", "-f", "-"], input=proc.stdout)

overrides = clusterversion.setdefault('spec', {}).setdefault('overrides', [])

# Merge the new override in if required
modified = False
for override in overrides:
    if override.get('name') == name and override.get('namespace') == namespace:
        if not override.get('unmanaged'):
            override['unmanaged'] = True
            modified=True
        break
else:
    overrides.append({
        "group": "apps/v1",
        "kind": "Deployment",
        "name": name,
        "namespace": namespace,
        "unmanaged": True
    })
    modified = True

# Apply the clusterversion if we modified overrides
if modified:
    subprocess.run(["oc", "apply", "-f", "-"],
                   input=json.dumps(clusterversion), text=True)
