#!/bin/bash

set -x
# start etcd
    source ./config.sh
    ETCD_IP=ETCD_NODE_IP

    # etcd
    etcd --name s-1 \
    --data-dir /tmp/etcd/s-1 \
    --listen-client-urls https://0.0.0.0:2379 \
    --advertise-client-urls "https://$ETCD_IP:2379" \
    --listen-peer-urls http://0.0.0.0:2380 \
    --initial-advertise-peer-urls http://0.0.0.0:2380 \
    --initial-cluster s-1=http://0.0.0.0:2380 \
    --initial-cluster-token tkn \
    --initial-cluster-state new \
    --cert-file=$CERTS_DIR/frededge1.crt \
    --key-file=$CERTS_DIR/frededge1.key \
    --client-cert-auth \
    --trusted-ca-file=$CERTS_DIR/ca.crt


set +x