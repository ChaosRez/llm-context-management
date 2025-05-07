#!/bin/bash

set -x

# OUTPUT_FILE="$1"
# DELAY="$2"
# BANDWIDTH_MBPS="$3"
# DELAY_CLIENT_EDGE="${4}"
# BANDWIDTH_CLIENT_EDGE_MBPS="${5}"
# DEPLOYMENT_MODE="${6}"
# TIMEOUT="${7}"
#
# if [  [ -z "$DELAY" ] || [ -z "$BANDWIDTH_MBPS" ] || [ -z "$DELAY_CLIENT_EDGE" ] || [ -z "$BANDWIDTH_CLIENT_EDGE_MBPS" ] || [ -z "$DEPLOYMENT_MODE" ] || [ -z "$TIMEOUT" ]; then
#     echo "missing parameters"
#     echo "usage: ./run-session.sh <delay> <bandwidth_mbps> <delay_client_edge> <bandwidth_client_edge_mbps> <deployment_mode> <timeout>"
#     exit 1
# fi
#
# # check that the deployment mode is valid
# # if [ "$DEPLOYMENT_MODE" != "cloud" ] && [ "$DEPLOYMENT_MODE" != "edge" ]; then
# if [ "$DEPLOYMENT_MODE" != "edge" ]; then
#     echo "invalid deployment mode"
#     exit 1
# fi

# get FReD config
# source ./config.sh
# #
# EDGE_IP_1=$EDGE_IP_1
# EDGE_IP_2=$EDGE_IP_2
#
# EDGE_INSTANCE_1=$EDGE_INSTANCE_1
# EDGE_INSTANCE_2=$EDGE_INSTANCE_2
#
# CLIENT_INSTANCE=$CLIENT_INSTANCE
# CERTS_DIR=$CERTS_DIR
#
# EDGE_NAME_1=$EDGE_ID_1
# EDGE_NAME_2=$EDGE_ID_2
#
# CLIENT_NAME=$CLIENT_ID
#
# # ssh "$EDGE_INSTANCE_1" docker system prune -f &
# # ssh "$EDGE_INSTANCE_2" docker system prune -f &
# # wait
#
# # generate some certificates
# CA_CERT="$CERTS_DIR/ca.crt"
#
# # etcd(edge1)
# ETCD_CERT="$CERTS_DIR/etcd.crt"
# ETCD_KEY="$CERTS_DIR/etcd.key"
#
# # edge1
# FREDEDGE1_CERT="$CERTS_DIR/frededge1.crt"
# FREDEDGE2_KEY="$CERTS_DIR/frededge1.key"
#
# # edge2
# FREDEDGE1_CERT="$CERTS_DIR/frededge2.crt"
# FREDEDGE2_KEY="$CERTS_DIR/frededge2.key"
#
#
# # run the edge
# home=$(ssh "$EDGE_INSTANCE" pwd)

# start etcd
## start etcd instance
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

### if deployment mode is edge, also start fred on edge
# first edge node
# if [ "$DEPLOYMENT_MODE" == "edge" ]; then
#         ./frednode \
#         --nodeID frededge1 \
#         --nase-host "130.149.253.140:2379" \
#         --nase-cached \
#         --adaptor badgerdb \
#         --badgerdb-path ./db \
#         --host 0.0.0.0:9001 \
#         --advertise-host "130.149.253.178:9001" \
#         --peer-host 0.0.0.0:5555 \
#         --peer-advertise-host "130.149.253.178:5555" \
#         --log-level debug \
#         --handler dev \
#         --cert cert/frededge1.crt \
#         --key cert/frededge1.key \
#         --ca-file cert/ca.crt \
#         --skip-verify \
#         --peer-cert cert/frededge1.crt \
#         --peer-key cert/frededge1.key \
#         --peer-ca cert/ca.crt \
#         --peer-skip-verify \
#         --nase-cert cert/frededge2.crt \
#         --nase-key cert/frededge2.key \
#         --nase-ca cert/ca.crt \
#         --nase-skip-verify \
#         --trigger-cert cert/frededge1.crt \
#         --trigger-key cert/frededge1.key \
#         --trigger-ca cert/ca.crt \
#         --trigger-skip-verify
# fi

# second edge node
if [ "$DEPLOYMENT_MODE" == "edge" ]; then
        ./frednode \
        --nodeID frededge2 \
        --nase-host "130.149.253.140:2379" \
        --nase-cached \
        --adaptor badgerdb \
        --badgerdb-path ./db \
        --host 0.0.0.0:9001 \
        --advertise-host "130.149.253.140:9001" \
        --peer-host 0.0.0.0:5555 \
        --peer-advertise-host "130.149.253.140:5555" \
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
fi




# DOCKERKV_HOST="$CLOUD_IP"
# if [ "$DEPLOYMENT_MODE" == "edge" ]; then
#     DOCKERKV_HOST="$EDGE_IP"
# fi
#
# ssh "$EDGE_INSTANCE_1" \
#     TF_BACKEND=dockerkv \
#     DOCKERKV_CERTS_DIR=${CERTS_DIR} \
#     DOCKERKV_CA_CERT_PATH=${CERTS_DIR}/ca.crt \
#     DOCKERKV_CA_KEY_PATH=${CERTS_DIR}/ca.key \
#     DOCKERKV_HOST="$DOCKERKV_HOST" \
#     DOCKERKV_PORT=9001 \
#     ./manager &

# start the experiment on the client=> alexandra will take care of the experiments :)
# python3 load.py <endpoint> <function> <requests> <threads> <frequency> <output>"
# RES_FILE="results.txt"

# ready to run!
# start_time=$(date +%s)
# echo "-------------------"
#
# echo "delay: $DELAY"
# echo "bandwidth: $BANDWIDTH_MBPS"
# echo "deployment mode: $DEPLOYMENT_MODE"
# echo "timeout: $TIMEOUT"
# echo "start time: $(date)"
# echo "-------------------"
#

# collect the logs
# curl "http://$EDGE_IP_1:8080/logs"
# curl "http://$EDGE_IP_2:8080/logs"

# destroy the machines
set +x

echo "to destroy the machines run: terraform destroy -auto-approve"
