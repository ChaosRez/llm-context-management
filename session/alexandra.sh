#!/bin/bash
set -x
source ./config.sh
EDGE_IP_1 = $EDGE_IP_1
EDGE_IP_2 = $EDGE_IP_2

    ./alexandra\
    --address "$EDGE_IP_2:10000" \
    --lighthouse :9001 \
    --ca-cert cert/ca.crt \
    --alexandra-key cert/alexandra.key \
    --clients-key cert/alexandra.key \
    --alexandra-cert cert/alexandra.crt \
    --clients-cert cert/alexandra.crt \
    --experimental

set +x