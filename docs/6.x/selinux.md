## SELinux

Starting with version 7, Gravity comes with SELinux support.
It is not a requirement to have SELinux enabled, but whenever the installer detects that
it runs on a SELinux-enabled node, it will automatically turn on SELinux support.

When operating with SELinux support on, the following changes:

 * Installer process automatically loads the policy and configures the local paths and ports necessary
for its operation. After bootstrapping, the installer will run confined in its own domain.

 * Planet container runs its services and all Kubernetes workloads confined - this means Docker will also
be configured to run with SELinux support on.

### Host Preparation

Before installing Gravity, you have to ensure that the user performing the installation has the privilege
to load policy modules - otherwise the installer will fail to bootstrap.

To check the SELinux status, run the follwing:
```sh
$ sestatus
SELinux status:                 enabled
Current mode:                   enforcing
Policy from config file:        targeted
...
```

Map a Linux user (here, `user`) to SELinux user (i.e. `staff_u`):
```sh
$ semanage login -a -s staff_u user
```

Switch to the `sysadm_r` role:
```sh
$ sudo -r sysadm_r -i
```

Alternatively, run the installer using the role `sysadm_r` and type `sysadm_t`:
```sh
$ runcon -r sysadm_r -t sysadm_t ./gravity install ...
```


### Upgrades

The upgrade only turns SELinux support for a cluster previously installed with SELinux support.


### Custom SELinux policies

It is not yet possible to bundle a custom SELinux policy in the cluster image. If you have custom domains
you'd need to make sure to load the policy on each node where necessary prior to installing the cluster image.


### OS Distribution Support

Currently the following distributions are supported:

| Distribution | Version |
|--------------|----------------|
| CentOS       | 7+            |
| RedHat       | 7+            |
|--------------|----------------|


### Troubleshooting
