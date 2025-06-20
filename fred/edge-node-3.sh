#!/bin/bash

set -x

source ./config.sh
EDGE_IP=$JETSON_IP
ETCD_IP=$ETCD_NODE_IP


        ./frednode \
        --nodeID fredjetson \
        --nase-host "$ETCD_IP:2379" \
        --nase-cached \
        --adaptor memory \
        --host 0.0.0.0:9001 \
        --advertise-host "$EDGE_IP:9001" \
        --peer-host 0.0.0.0:5555 \
        --peer-advertise-host "$EDGE_IP:5555" \
        --log-level debug \
        --handler dev \
        --cert $CERTS_DIR/fredjetson.crt \
        --key $CERTS_DIR/fredjetson.key \
        --ca-file $CERTS_DIR/ca.crt \
        --skip-verify \
        --peer-cert $CERTS_DIR/fredjetson.crt \
        --peer-key $CERTS_DIR/fredjetson.key \
        --peer-ca $CERTS_DIR/ca.crt \
        --peer-skip-verify \
        --nase-cert $CERTS_DIR/fredjetson.crt \
        --nase-key $CERTS_DIR/fredjetson.key \
        --nase-ca $CERTS_DIR/ca.crt \
        --nase-skip-verify \
        --trigger-cert $CERTS_DIR/fredjetson.crt \
        --trigger-key $CERTS_DIR/fredjetson.key \
        --trigger-ca $CERTS_DIR/ca.crt \
        --trigger-skip-verify

set +x