#!/bin/bash
set -x
source ./config.sh
EDGE_IP_1=$EDGE_IP_1
EDGE_IP_2=$EDGE_IP_2

    ./alexandra\
    --address "$EDGE_IP_2:10000" \
    --lighthouse :9001 \
    --ca-cert cert/ca.crt \
    --alexandra-key cert/frededge2.key \
    --clients-key cert/frededge2.key \
    --alexandra-cert cert/frededge2.crt \
    --clients-cert cert/frededge2.crt \
    --experimental

set +x