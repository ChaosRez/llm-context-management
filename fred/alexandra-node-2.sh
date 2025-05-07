#!/bin/bash
set -x
source ./config.sh
EDGE_IP_1=$EDGE_IP_1
EDGE_IP_2=$EDGE_IP_2

    ./alexandra\
    --address "$EDGE_IP_1:10000" \
    --lighthouse :9001 \
    --ca-cert cert/ca.crt \
    --alexandra-key cert/frededge1.key \
    --clients-key cert/frededge1.key \
    --alexandra-cert cert/frededge1.crt \
    --clients-cert cert/frededge1.crt \
    --experimental

set +x