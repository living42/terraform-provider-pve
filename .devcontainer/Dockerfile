FROM golang:1.22

RUN <<EOF
set -xeu

wget -O- https://apt.releases.hashicorp.com/gpg | gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
. /etc/os-release
echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com ${VERSION_CODENAME} main" | tee /etc/apt/sources.list.d/hashicorp.list
apt-get update && apt-get install 'terraform=1.7.4-1'
EOF
