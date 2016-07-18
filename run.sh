#!/bin/bash

set -e

VOLUME_NAME=/dev/disk/by-id/scsi-0DO_Volume_$(hostname)-vol1
PART_PATH=${VOLUME_NAME}-part1
MNT_DIR=/mnt/vol-1

if [[ -f $VOLUME_NAME ]]; then
  echo "could not find volume ${VOLUME_NAME}"
  exit -1
fi

apt-get update
apt-get install -q -y fio bc

curl -O -sSL https://s3.pifft.com/vtest/bin/fio.sh
chmod +x fio.sh
curl -O -sSL https://s3.pifft.com/vtest/bin/fioparse.sh
chmod +x fioparse.sh
curl -o /usr/local/bin/mc -sSL https://dl.minio.io/client/mc/release/linux-amd64/mc
chmod +x /usr/local/bin/mc

parted -s $VOLUME_NAME mklabel gpt
parted -s -a opt $VOLUME_NAME mkpart primary ext4 0% 100%

sync; sync; sync

if [ ! -b $PART_PATH ]; then
  echo "could not find partition"
  exit 1
fi

mkfs.ext4 -F $PART_PATH
sudo mkdir -p ${MNT_DIR}
echo "${PART_PATH} ${MNT_DIR} ext4 defaults,nofail,discard 0 2" | tee -a /etc/fstab
mount -a

./fio.sh -b $(which fio) -w ${MNT_DIR} -f