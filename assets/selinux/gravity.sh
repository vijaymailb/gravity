#!/bin/sh -e

DIRNAME=`dirname $0`
cd $DIRNAME
USAGE="$0 [ --update ]"
if [ `id --user` != 0 ]; then
  echo 'You must be root to run this script'
  exit 1
fi

if [ $# -eq 1 ]; then
  if [ "$1" = "--update" ] ; then
    time=`ls -l --time-style="+%x %X" gravity.te | awk '{ printf "%s %s", $6, $7 }'`
    rules=`ausearch --start $time --message avc --raw --context gravity`
    if [ x"$rules" != "x" ] ; then
      echo "Found AVCs to update policy with"
      echo -e "$rules" | audit2allow --reference
      echo "Do you want these changes added to policy [y/n]?"
      read ANS
      if [ "$ANS" = "y" -o "$ANS" = "Y" ] ; then
        echo "Updating policy"
        echo -e "$rules" | audit2allow --reference >> gravity.te
        # Fall though and rebuild policy
      else
        exit 0
      fi
    else
      echo "No new AVCs found"
      exit 0
  fi
else
  echo -e $USAGE
  exit 1
fi
elif [ $# -ge 2 ] ; then
  echo -e $USAGE
  exit 1
fi

echo "Building and loading Policy"
set -x
make -f /usr/share/selinux/devel/Makefile gravity.pp || exit
/usr/sbin/semodule --install gravity.pp

# Generate a man page off the installed module
sepolicy manpage -p . -d gravity_t
if [ -f /usr/bin/gravity ]; then
  # Fixing the file context on /usr/bin/gravity
  /sbin/restorecon -F -R -v /usr/bin/gravity
fi
# Fixing the file context on /var/lib/gravity
/sbin/restorecon -F -R -v /var/lib/gravity
# Generate a rpm package for the newly generated policy

pwd=$(pwd)
rpmbuild \
  --define "_sourcedir ${pwd}" \
  --define "_specdir ${pwd}" \
  --define "_builddir ${pwd}" \
  --define "_srcrpmdir ${pwd}" \
  --define "_rpmdir ${pwd}" \
  --define "_buildrootdir ${pwd}/.build" \
  -ba gravity_selinux.spec
