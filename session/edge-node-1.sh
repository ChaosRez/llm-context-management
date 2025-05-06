#!/bin/bash

set -x
# reza-node
       ./frednode \
        --nodeID frededge1 \
        --nase-host "130.149.253.140:2379" \
        --nase-cached \
        --adaptor badgerdb \
        --badgerdb-path ./db \
        --host 0.0.0.0:9001 \
        --advertise-host "130.149.253.178:9001" \
        --peer-host 0.0.0.0:5555 \
        --peer-advertise-host "130.149.253.178:5555" \
        --log-level debug \
        --handler dev \
        --cert cert/frededge1.crt \
        --key cert/frededge1.key \
        --ca-file cert/ca.crt \
        --skip-verify \
        --peer-cert cert/frededge1.crt \
        --peer-key cert/frededge1.key \
        --peer-ca cert/ca.crt \
        --peer-skip-verify \
        --nase-cert cert/frededge2.crt \
        --nase-key cert/frededge2.key \
        --nase-ca cert/ca.crt \
        --nase-skip-verify \
        --trigger-cert cert/frededge1.crt \
        --trigger-key cert/frededge1.key \
        --trigger-ca cert/ca.crt \
        --trigger-skip-verify


set +x