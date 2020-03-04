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

Next, the Linux user to perform the installation needs to be mapped to a SELinux user.
Installer needs needs to run with an administrative role capable of loading the policy - for example, `sysadm_r`.

To check exsisting mappings, use the following:

```sh
$ semanage login -l
```

To map the Linux user `user` to a SELinux user `staff_u`, do the following:
```sh
$ semanage login -a -s staff_u user
```
or modify the mapping with:
```
$ semanage login -m -s staff_u user
```

Switch to the `sysadm_r` role:
```sh
$ sudo -r sysadm_r -i
```

Alternatively, directly run the installer using the role `sysadm_r` and type `sysadm_t`:
```sh
$ runcon -r sysadm_r -t sysadm_t ./gravity install ...
```

### Installation

The install operation was not changed except for the new implicit bootstrapping step that:

  * loads the Gravity SELinux policy module
  * creates local port bindings for Gravity and Kubernetes-specific ports
  * creates local file contexts for paths used during the install

To start the installation, use the `gravity install` command as usual:

```sh
$ gravity install ...
 Bootstrapping installer for SELinux
 ...
```

Likewise, on the joining node:

```sh
$ gravity join ...
 Bootstrapping installer for SELinux
 ...
```

SELinux support can be turnd off explicitly with `--no-selinux` specified on the `gravity isntall` command line:

```sh
$ gravity install --no-selinux ...
```

This needs to be done explicitly for joining nodes as well if using the command line interface:

```sh
$ gravity join --no-selinux ...
```


### Upgrades

The upgrade runs with SELinux support only if the cluster was previously installed with SELinux support.


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

If the installer fails, pay attention to the errors about denied permissions which might be the indicator of an SELinux issue.

Unfortunately, SELinux has a UI problem as it might not be immediately obvious whether a particular 'permission denied' refers to the Linux denying
DAC (Discretionary Access Control) access or SELinux has denied access - SELinux verifications happen only after DAC check.

In order to check whether the denial is specific to SELinux, one needs to look into the SELinux audit log.
While the audit log (found in `/var/log/audit/audit.log`) can be inspected directly, it is easier to use a tool that can interpret the log and present
the results in a more readable way.

For example, to check for all relevant SELinux denials, use the following:

```sh
$ sealert -ts recent -t AVC, SELINUX_AVC, 
```
