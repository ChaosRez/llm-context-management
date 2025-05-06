#!/bin/bash

set -x
# start etcd
    etcd --name s-1 \
    --data-dir /tmp/etcd/s-1 \
    --listen-client-urls https://0.0.0.0:2379 \
    --advertise-client-urls "https://130.149.253.140:2379" \
    --listen-peer-urls http://0.0.0.0:2380 \
    --initial-advertise-peer-urls http://0.0.0.0:2380 \
    --initial-cluster s-1=http://0.0.0.0:2380 \
    --initial-cluster-token tkn \
    --initial-cluster-state new \
    --cert-file=cert/etcd.crt \
    --key-file=cert/etcd.key \
    --client-cert-auth \
    --trusted-ca-file=cert/ca.crt


set +x