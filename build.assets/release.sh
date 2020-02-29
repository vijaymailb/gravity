#!/bin/bash
set -e

TEMP_DIR="$(mktemp -d)"
trap "rm -rf ${TEMP_DIR}" exit

# Add assets to the release tarball
#
# Any content added here should also be added in the make release
# target dependencies
cp ${TSH_OUT} ${TELE_OUT} install.sh ../LICENSE ${TEMP_DIR}
cp release-tarball-README.md ${TEMP_DIR}/README.md
../version.sh > ${TEMP_DIR}/VERSION

tar -C ${TEMP_DIR} -zcvf ${RELEASE_OUT} .
