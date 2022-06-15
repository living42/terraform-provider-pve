#!/bin/bash
set -xeu

# NOTE Increase VMID if you change VERSION

VERSION=20.04
DATE=20220610
CODE_NAME=focal
IMAGE_NAME=ubuntu-$VERSION-server-cloudimg-amd64.img
IMAGE_URL=https://cloud-images.ubuntu.com/releases/$CODE_NAME/release-$DATE/$IMAGE_NAME
CHECKSUM_URL=https://cloud-images.ubuntu.com/releases/$CODE_NAME/release-$DATE/SHA256SUMS

VMID=${VMID:-9002}

TEMPLATE_NAME=ubuntu-$VERSION-$DATE

mkdir -p ubuntu-vm-template-$TEMPLATE_NAME
trap "rm -r $PWD/ubuntu-vm-template-$TEMPLATE_NAME" EXIT

cd ubuntu-vm-template-$TEMPLATE_NAME

wget $CHECKSUM_URL -O SHA256SUMS
wget --progress dot:giga $IMAGE_URL -O $IMAGE_NAME
sha256sum -c SHA256SUMS --ignore-missing

qm create $VMID --name $TEMPLATE_NAME -ostype l26 -cpu cputype=host --memory 2048 --net0 virtio,bridge=vmbr0 --ipconfig0 ip=dhcp

qm set $VMID -ostype l26

qm importdisk $VMID $IMAGE_NAME local -format qcow2

qm set $VMID --scsihw virtio-scsi-pci --scsi0 /var/lib/vz/images/$VMID/vm-$VMID-disk-0.qcow2

# weird timeout error when first try, but success on second try
qm resize $VMID scsi0 32G || qm resize $VMID scsi0 32G

qm set $VMID --ide2 local:cloudinit --boot c --bootdisk scsi0

cat <<EOF > /var/lib/vz/snippets/vm-$VMID-cloud-init-user.yaml
#cloud-config
packages:
  # We want to us Packer to build another templates, it depends on qemu-guest-agent
  # to detect VM's IP address
  - qemu-guest-agent

runcmd:
  # Default iface options will try to configure interface using DHCP
  # then cloud-init process will configure again, the result, vm will
  # got two IP address, to avoid this, we need remove these default options
  - sed -i -e 's/iface eth/# iface eth/g' -e 's/allow-hotplug eth/# allow-hotplug eth/g' /etc/network/interfaces

  # Disable IPv6 to avoid trouble
  - |
    cat <<EOF > /etc/sysctl.d/11-disable-ipv6.conf
    net.ipv6.conf.all.disable_ipv6 = 1
    net.ipv6.conf.default.disable_ipv6 = 1
    net.ipv6.conf.lo.disable_ipv6 = 1
    net.ipv6.conf.eth0.disable_ipv6 =1
    EOF

    cat <<EOF > /etc/default/grub.d/51-disable-ipv6.cfg
    GRUB_CMDLINE_LINUX_DEFAULT="$$GRUB_CMDLINE_LINUX_DEFAULT ipv6.disable=1"
    EOF

    update-grub

  # PVE forces upgrade package at first boot via cloud-init
  # Disable package-update-upgrade-install, thus boot from template will much faster, more predicable
  - |
    sed -i 's/- package-update-upgrade-install/# - package-update-upgrade-install/g' /etc/cloud/cloud.cfg

power_state:
  mode: poweroff
EOF
qm set $VMID --cicustom "user=local:snippets/vm-$VMID-cloud-init-user.yaml"
qm start $VMID
# wait vm shutdown
qm wait $VMID -timeout 300
qm set $VMID --agent enabled=1,type=virtio

rm /var/lib/vz/snippets/vm-$VMID-cloud-init-user.yaml

# Don't forget to clear custom userdata, otherwise we can't set password by using --cipassword
qm set $VMID --cicustom ""

qm template $VMID
