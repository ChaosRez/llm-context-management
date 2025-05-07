#!/bin/bash

set -x
# start etcd
<<<<<<< Updated upstream:fred/etcd.sh
    source ./config.sh
    EDGE_IP=$EDGE_IP_1
    ETCD_IP=$EDGE_IP_2
=======
source ./config.sh
EDGE_IP_1=$EDGE_IP_1
EDGE_IP_2=$EDGE_IP_2
>>>>>>> Stashed changes:session/etcd.sh

    etcd --name s-1 \
    --data-dir /tmp/etcd/s-1 \
    --listen-client-urls https://0.0.0.0:2379 \
<<<<<<< Updated upstream:fred/etcd.sh
    --advertise-client-urls "https://$ETCD_IP:2379" \
=======
    --advertise-client-urls "https://$EDGE_IP_2:2379" \
>>>>>>> Stashed changes:session/etcd.sh
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