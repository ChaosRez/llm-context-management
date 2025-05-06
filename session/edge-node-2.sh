#!/bin/bash

set -x
source ./config.sh
EDGE_IP = $EDGE_IP_2

# # minghe-node
        ./frednode \
        --nodeID frededge2 \
        --nase-host "$EDGE_IP:2379" \
        --nase-cached \
        --adaptor badgerdb \
        --badgerdb-path ./db \
        --host 0.0.0.0:9001 \
        --advertise-host "$EDGE_IP:9001" \
        --peer-host 0.0.0.0:5555 \
        --peer-advertise-host "$EDGE_IP:5555" \
        --log-level debug \
        --handler dev \
        --cert cert/frededge2.crt \
        --key cert/frededge2.key \
        --ca-file cert/ca.crt \
        --skip-verify \
        --peer-cert cert/frededge2.crt \
        --peer-key cert/frededge2.key \
        --peer-ca cert/ca.crt \
        --peer-skip-verify \
        --nase-cert cert/frededge2.crt \
        --nase-key cert/frededge2.key \
        --nase-ca cert/ca.crt \
        --nase-skip-verify \
        --trigger-cert cert/frededge2.crt \
        --trigger-key cert/frededge2.key \
        --trigger-ca cert/ca.crt \
        --trigger-skip-verify

set +x