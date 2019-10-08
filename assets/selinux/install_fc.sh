#/bin/bash

if [ `id --user` != 0 ]; then
  echo 'You must be root to run this script'
  exit 1
fi

DIR="$( cd "$(dirname "$0")" ; pwd -P )"

semanage fcontext -a -t gravity_exec_t "${DIR}/gravity"
restorecon -Rv "${DIR}/gravity"
