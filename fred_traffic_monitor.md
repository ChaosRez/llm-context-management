# FReD Traffic Capture for DisCEdge Experiments

This document explains how to capture and analyze TCP traffic for FReD (distributed DB) in the **DisCEdge: Distributed Context Management for Large Language Models at the Edge** paper project. This is the prototype implementation repository.

The guide helps you interpret `tcpdump` output to differentiate between packets carrying your application's data and packets that are part of the TCP protocol's overhead.

## Citation

If you use this traffic monitoring script in your research or project, please cite our paper:

```
@article{malekabbasi2025discedge,
      title={DisCEdge: Distributed Context Management for Large Language Models at the Edge}, 
      author={Mohammadreza Malekabbasi and Minghe Wang and David Bermbach},
      year={2025},
      url={https://arxiv.org/abs/2511.22599}, 
}
```

**Please acknowledge this work when using or adapting this script for your experiments.**

## Prerequisites

Before using the FReD traffic monitoring script, you need to install the following tools:

### tcpdump
- **Linux (Debian/Ubuntu)**: `sudo apt install tcpdump`
- **Linux (RHEL/CentOS)**: `sudo yum install tcpdump`
- **macOS**: Usually pre-installed. If not: `brew install tcpdump`

### tshark (Wireshark CLI)
- **Linux (Debian/Ubuntu)**: `sudo apt install tshark`
- **Linux (RHEL/CentOS)**: `sudo yum install wireshark`
- **macOS**: `brew install wireshark`

After installing tshark on Linux, you may need to add your user to the `wireshark` group:
```bash
sudo usermod -aG wireshark $USER
```
Then log out and log back in for the changes to take effect.

## Running the Traffic Capture Experiments

### Basic Usage

```bash
./fred_traffic_monitor.sh <mode> [duration] [interface]
```

### Parameters

1. **`mode`** (required): Experiment mode identifier
   - Used to label the output CSV file
   - Examples: `tokenized`, `raw`, `baseline`, `caching_enabled`, etc.
   - This helps organize different experimental runs

2. **`duration`** (optional, default: 20 seconds): Capture duration in seconds
   - How long to capture traffic
   - Recommended: 20-60 seconds depending on your workload

3. **`interface`** (optional, auto-detected): Network interface to monitor
   - If not specified, the script auto-detects the default network interface
   - Common interfaces:
     - Linux: `eth0`, `wlan0`, `enp0s3`
     - macOS: `en0`, `en1`
   - Use `ip link show` (Linux) or `ifconfig` (macOS) to list available interfaces

### Example Commands

**Basic experiment (auto-detect interface, 20 second duration):**
```bash
./fred_traffic_monitor.sh tokenized
```

**Extended capture duration (60 seconds):**
```bash
./fred_traffic_monitor.sh raw 60
```

**Specify network interface explicitly:**
```bash
./fred_traffic_monitor.sh caching_enabled 30 eth0
```

**Multiple experimental runs:**
```bash
# Baseline without context caching
./fred_traffic_monitor.sh baseline_no_cache 30

# With context caching enabled
./fred_traffic_monitor.sh with_cache 30

# Different prompt sizes
./fred_traffic_monitor.sh small_prompt 30
./fred_traffic_monitor.sh large_prompt 30
```

### Output

The script generates a CSV file in `testdata/net_log/` with the following format:

```
testdata/net_log/YYYYMMDD_HHMMSS_traffic_<mode>.csv
```

**CSV columns:**
- `timestamp`: UTC timestamp with millisecond precision
- `source_ip`: Source IP address of the packet
- `type`: TCP flags (e.g., `A` for ACK, `PA` for PSH+ACK)
- `bytes`: Total packet size in bytes
- `packet_number`: Sequential packet number in the capture

**Console output:**
```
📡 Capturing TCP traffic on port 5555 via interface: eth0 (for 30 seconds)
⏳ Running tcpdump...
✅ Capture complete. Analyzing...
📊 Analyzing and generating detailed log...
Total Packets: 1234
Total Bytes:   567890
📈 Detailed results saved to testdata/net_log/20240115_143022_traffic_tokenized.csv
```

### Typical Experiment Workflow

1. **Start your FReD server/client** on the test machines
2. **Launch the traffic capture** in a separate terminal:
   ```bash
   ./fred_traffic_monitor.sh experiment_name 60
   ```
3. **Execute your workload** (send requests, perform operations)
4. **Wait for capture to complete** (or stop early with Ctrl+C if needed)
5. **Analyze the generated CSV file** for packet counts, bytes transferred, and timing

### Notes

- The script requires `sudo` privileges for packet capture
- FReD traffic is monitored on **port 5555** (hardcoded in the script)
- Temporary capture files are stored in `/tmp/` and automatically cleaned up
- The script is compatible with both Linux and macOS