#!/bin/bash

set -x

# TODO: check FReD:
# 1. 'copy certs to instances' maybe is not necessary, at least need modifications
# 2. if the etcd(current for cloud) is necessary or not?
# 3. if the client preparation is necessary or not


# get the ip addresses
source ./config.sh

EDGE_IP_1 = $EDGE_IP_1
EDGE_IP_2 = $EDGE_IP_2
EDGE_INSTANCE_1 = $EDGE_INSTANCE_1
EDGE_INSTANCE_2 = $EDGE_INSTANCE_2

CLIENT_INSTANCE = $CLIENT_INSTANCE
CERTS_DIR = $CERTS_DIR

echo "Configurations:"
echo "EDGE_IP_1: $EDGE_IP_1"
echo "EDGE_IP_2: $EDGE_IP_2"
echo "CLIENT_IP: $CLIENT_IP"
echo "EDGE_INSTANCE_1: $EDGE_INSTANCE_1"
echo "EDGE_INSTANCE_2: $EDGE_INSTANCE_2"
echo "CLIENT_INSTANCE: $CLIENT_INSTANCE"
echo "The certificates are saved in: $CERTS_DIR"

# generate certificates for fred(edge nodes)
mkdir -p "$CERTS_DIR"
openssl genrsa -out "$CERTS_DIR/ca.key" 2048

CA_CERT="$CERTS_DIR/ca.crt"
# openssl req -x509 -new -nodes -key "$CERTS_DIR/ca.key" -days 1825 -sha512 -out "$CA_CERT" -subj "/C=DE/L=Berlin/O=OpenFogStack/OU=enoki"
openssl req -x509 -new -nodes -key "$CERTS_DIR/ca.key" -days 1825 -sha512 -out "$CA_CERT" -subj "/C=DE/L=Berlin/O=Paper/OU=session"

# ./gen-cert.sh "$CERTS_DIR" etcd "$CLOUD_IP"
#
./gen-cert.sh "$CERTS_DIR" frededge "$EDGE_IP_1"
./gen-cert.sh "$CERTS_DIR" frededge "$EDGE_IP_2"

# copy the certificate to edge instance
# scp -r "$CERTS_DIR" "$CLOUD_INSTANCE:~"

scp -r "$CERTS_DIR" "$EDGE_INSTANCE_1:~"
scp -r "$CERTS_DIR" "$EDGE_INSTANCE_2:~"


# install docker
ssh "$EDGE_INSTANCE_1" curl -fsSL https://get.docker.com -o get-docker.sh
ssh "$EDGE_INSTANCE_1" sudo sh get-docker.sh &

ssh "$EDGE_INSTANCE_2" curl -fsSL https://get.docker.com -o get-docker.sh
ssh "$EDGE_INSTANCE_2" sudo sh get-docker.sh &
wait

# prep the edge
user=$(ssh "$EDGE_INSTANCE_1" whoami)
ssh "$EDGE_INSTANCE_1" sudo usermod -aG docker "$user"

user=$(ssh "$EDGE_INSTANCE_2" whoami)
ssh "$EDGE_INSTANCE_2" sudo usermod -aG docker "$user"

# prep the client
ssh "$CLIENT_INSTANCE" sudo apt-get update
ssh "$CLIENT_INSTANCE" sudo apt-get install -y python3-pip
ssh "$CLIENT_INSTANCE" python3 -m pip install tqdm==4.65.0

set +x

echo "to destroy the machines run: terraform destroy -auto-approve"
