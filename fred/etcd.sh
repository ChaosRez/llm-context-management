#!/bin/bash

set -x
# start etcd
    source ./config.sh
    EDGE_IP=$EDGE_IP_1
    ETCD_IP=$EDGE_IP_2

    etcd --name s-1 \
    --data-dir /tmp/etcd/s-1 \
    --listen-client-urls https://0.0.0.0:2379 \
    --advertise-client-urls "https://$ETCD_IP:2379" \
    --listen-peer-urls http://0.0.0.0:2380 \
    --initial-advertise-peer-urls http://0.0.0.0:2380 \
    --initial-cluster s-1=http://0.0.0.0:2380 \
    --initial-cluster-token tkn \
    --initial-cluster-state new \
    --cert-file=cert/frededge2.crt \
    --key-file=cert/frededge2.key \
    --client-cert-auth \
    --trusted-ca-file=cert/ca.crt


set +x