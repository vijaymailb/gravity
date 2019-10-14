#/bin/bash

# if [ `id --user` != 0 ]; then
#   echo 'You must be root to run this script'
#   exit 1
# fi

DIR="$( cd "$(dirname "$0")" ; pwd -P )"

function setup_file_contexts {
  # Label the installer
  semanage fcontext -a -t gravity_exec_t "${DIR}/gravity"
  # Label the current directory for installer
  semanage fcontext -a -t gravity_installer_t "${DIR}(/.*)?"
  # Apply labels
  restorecon -Rv "${DIR}"
}

function setup_ports {
  # https://danwalsh.livejournal.com/10607.html
  semanage port -a -t gravity_installer_port_t -p tcp 61009-61010
  semanage port -a -t gravity_installer_port_t -p tcp 61022-61025
  semanage port -a -t gravity_installer_port_t -p tcp 61080
  # Cluster ports
  semanage port -a -t gravity_port_t -p tcp 4242
  semanage port -a -t gravity_port_t -p tcp 3012
  semanage port -a -t gravity_port_t -p tcp 7373
  semanage port -a -t gravity_port_t -p tcp 7496
  semanage port -a -t gravity_port_t -p tcp 7575
  semanage port -a -t gravity_port_t -p tcp 32009
  semanage port -a -t gravity_port_t -p tcp 3008
  semanage port -a -t gravity_port_t -p tcp 3022
  semanage port -a -t gravity_port_t -p tcp 3080
  semanage port -a -t gravity_kubernetes_port_t -p tcp 2379-2380
  # these ports are reserved and overridden in the policy
  # semanage port -a -t gravity_kubernetes_port_t -p tcp 4001
  # semanage port -a -t gravity_kubernetes_port_t -p tcp 7001
  # semanage port -a -t gravity_kubernetes_port_t -p tcp 6443
  semanage port -a -t gravity_kubernetes_port_t -p tcp 10248-10255

  # TODO: vxlan port separate type?
  semanage port -a -t gravity_vxlan_port_t -p tcp 8472
}

# setup_file_contexts
setup_ports

# TODO

