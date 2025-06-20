#!/bin/bash

set -x

source ./config.sh
EDGE_IP=$EDGE_IP_1
ETCD_IP=$ETCD_NODE_IP

# reza-node
       ./frednode \
        --nodeID frededge1 \
        --nase-host "$ETCD_IP:2379" \
        --nase-cached \
        --adaptor memory \
        --host 0.0.0.0:9001 \
        --advertise-host "$EDGE_IP:9001" \
        --peer-host 0.0.0.0:5555 \
        --peer-advertise-host "$EDGE_IP:5555" \
        --log-level debug \
        --handler dev \
        --cert $CERTS_DIR/frededge1.crt \
        --key $CERTS_DIR/frededge1.key \
        --ca-file $CERTS_DIR/ca.crt \
        --skip-verify \
        --peer-cert $CERTS_DIR/frededge1.crt \
        --peer-key $CERTS_DIR/frededge1.key \
        --peer-ca $CERTS_DIR/ca.crt \
        --peer-skip-verify \
        --nase-cert $CERTS_DIR/frededge2.crt \
        --nase-key $CERTS_DIR/frededge2.key \
        --nase-ca $CERTS_DIR/ca.crt \
        --nase-skip-verify \
        --trigger-cert $CERTS_DIR/frededge1.crt \
        --trigger-key $CERTS_DIR/frededge1.key \
        --trigger-ca $CERTS_DIR/ca.crt \
        --trigger-skip-verify


set +x