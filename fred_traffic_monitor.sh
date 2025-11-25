#!/bin/bash
#
# FReD Traffic Monitor
# Part of DisCEdge: Distributed Context Management for Large Language Models at the Edge
#
# Copyright (c) 2024 Reza Malek
# Licensed under the MIT License
#
# This script captures and analyzes TCP traffic for FReD experiments.
#

if [ -z "$1" ]; then
    echo "Usage: $0 <mode> [duration] [interface]"
    echo "Example: $0 tokenized"
    echo "Example: $0 raw 30 en0"
    echo ""
    echo "Captures FReD traffic on port $PORT for DisCEdge experiments."
    exit 1
fi

sudo -v

MODE=$1
PORT=5555
DURATION=${2:-20}

INTERFACE=${3:-$(ip route get 8.8.8.8 2>/dev/null | awk '{print $5}' | head -1)}
[[ "$OSTYPE" == "darwin"* ]] && INTERFACE=${3:-$(route get default | awk '/interface:/{print $2}')}

if [ -z "$INTERFACE" ]; then
    echo "❌ Could not determine network interface. Please specify one."
    echo "Usage: $0 <mode> [duration] [interface]"
    exit 1
fi

CAP_FILE="/tmp/tcp_port_${PORT}.pcap"

echo "📡 Capturing TCP traffic on port $PORT via interface: $INTERFACE (for $DURATION seconds)"
echo "⏳ Running tcpdump..."

sudo tcpdump -n -i "$INTERFACE" tcp port $PORT -w "$CAP_FILE" >/dev/null 2>&1 &
TCPDUMP_PID=$!
sleep "$DURATION"
sudo kill -INT "$TCPDUMP_PID"

echo "✅ Capture complete. Analyzing..."

if [ ! -s "$CAP_FILE" ]; then
    echo "⚠️  Capture file is empty. No traffic was captured on port $PORT."
    sudo rm -f "$CAP_FILE"
    exit 0
fi

if ! command -v tshark >/dev/null; then
    echo "⚠️  'tshark' is required. Install it via:"
    echo "    Debian: sudo apt install tshark"
    echo "    macOS: brew install wireshark"
    exit 1
fi

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_DIR="testdata/net_log"
mkdir -p "$OUTPUT_DIR"
CSV_FILE="${OUTPUT_DIR}/${TIMESTAMP}_traffic_${MODE}.csv"
echo "timestamp,source_ip,type,bytes,packet_number" > "$CSV_FILE"

echo "📊 Analyzing and generating detailed log..."
analysis_result=$( { tshark -r "$CAP_FILE" -Y "tcp.port == $PORT" -T fields -E separator=, -e frame.time_epoch -e ip.src -e tcp.flags.str -e frame.len 2>/dev/null | awk -v ostype="$OSTYPE" -F, '
BEGIN {
    packet_count = 0;
    byte_sum = 0;
}
{
    packet_count++;
    byte_sum += $4;

    epoch_sec = int($1);

    if (ostype ~ /^darwin/) {
        cmd = "date -r " epoch_sec " -u +\"%Y-%m-%dT%H:%M:%S\"";
    } else {
        cmd = "date -d @" epoch_sec " -u +\"%Y-%m-%dT%H:%M:%S\"";
    }

    if ( (cmd | getline timestamp) > 0 ) {
        # Append milliseconds
        split($1, parts, ".");
        milliseconds = sprintf(".%03d", substr(parts[2], 1, 3));
        timestamp = timestamp milliseconds "Z";
    } else {
        timestamp = "date_conversion_error";
    }
    close(cmd);

    print timestamp "," $2 "," $3 "," $4 "," packet_count;
}
END {
    print packet_count "," byte_sum > "/dev/stderr";
}' >> "$CSV_FILE"; } 2>&1 )

total_packets=$(echo "$analysis_result" | cut -d, -f1)
total_bytes=$(echo "$analysis_result" | cut -d, -f2)

echo "Total Packets: $total_packets"
echo "Total Bytes:   $total_bytes"
echo "📈 Detailed results saved to $CSV_FILE"

sudo rm -f "$CAP_FILE"
