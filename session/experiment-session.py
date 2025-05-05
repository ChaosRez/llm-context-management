# todo:
#  use fred for distributed storage, i.e., when location changes, fred should be able to create a replica node automatically.
#   so, here what should I do to use fred?
#  0. there is a session-client and it start a session.
#  1. prepare certificates for FReD when the this session is created, (take pre-simple.sh as a reference)
#  2. run FReD when preparation is done. (take run-simple.sh as a reference)
#  3. if the session-client moves to a new location, FReD should be able to handle the replication automatically.
#  4. the whole process sounds like a new python script called 'experiment-experiment-session.py'
import os
import random
import subprocess


class Experiment:
    def __init__(
            self,
            function: str,
            parameter: str,
            threads: int,
            load_requests: int,
            load_threads: int,
            load_frequency: int,
            delay_ms: int,
            bandwidth_mbps: int,
            delay_ms_client_edge: int,
            bandwidth_mbps_client_edge: int,
            deployment_mode: str,
            timeout: int,
            repeat: int,
            results_dir: str,
            output_dir: str,
    ):
        self.function = function
        self.parameter = parameter
        self.threads = threads
        self.load_requests = load_requests
        self.load_threads = load_threads
        self.load_frequency = load_frequency
        self.delay_ms = delay_ms
        self.bandwidth_mbps = bandwidth_mbps
        self.delay_ms_client_edge = delay_ms_client_edge
        self.bandwidth_mbps_client_edge = bandwidth_mbps_client_edge
        self.deployment_mode = deployment_mode
        self.timeout = timeout

        self.results_file = os.path.join(
            results_dir,
            f"{function}-{parameter}-{threads}-{load_requests}-{load_threads}-{load_frequency}-{delay_ms}-{bandwidth_mbps}-{delay_ms_client_edge}-{bandwidth_mbps_client_edge}-{deployment_mode}-{repeat}.csv",
        )

        self.output_file = os.path.join(
            output_dir,
            f"{function}-{parameter}-{threads}-{load_requests}-{load_threads}-{load_frequency}-{delay_ms}-{bandwidth_mbps}-{delay_ms_client_edge}-{bandwidth_mbps_client_edge}-{deployment_mode}-{repeat}.txt",
        )

    def run(self) -> None:
        # if the results file exists already, we can skip this experiment
        if os.path.exists(self.results_file):
            print(f"Skipping experiment: {self.results_file}")
            return

        print(f"Running experiment: {self.results_file}")
        print(f"Output: {self.output_file}")

        with open(self.output_file, "w") as f:
            subprocess.run(
                [
                    "./run-simple.sh",
                    self.function,
                    str(self.parameter),
                    str(self.threads),
                    str(self.load_requests),
                    str(self.load_threads),
                    str(self.load_frequency),
                    self.results_file,
                    str(self.delay_ms),
                    str(self.bandwidth_mbps),
                    str(self.delay_ms_client_edge),
                    str(self.bandwidth_mbps_client_edge),
                    self.deployment_mode,
                    str(self.timeout),
                ],
                stdout=f,
                stderr=f,
            )

        print(f"Finished experiment: {self.results_file}")

        # give stats
        # print results
        with open(self.results_file, "r") as f:
            lines = f.readlines()

            # remove header
            lines = [line.strip() for line in lines[1:]]

            # get mean, median, 95th, 99th
            times = [
                float(line.split(",")[1]) - float(line.split(",")[0]) for line in lines
            ]
            times.sort()

            mean = sum(times) / len(times)
            median = times[len(times) // 2]
            p95 = times[int(len(times) * 0.95)]
            p99 = times[int(len(times) * 0.99)]

            print(f"mean: {mean:.3f}s")
            print(f"median: {median:.3f}s")
            print(f"95th: {p95:.3f}s")
            print(f"99th: {p99:.3f}s")



if __name__ == "__main__":
    # functions and inputs
    functions: typing.Dict[str, typing.List[typing.Union[None, int]]] = {
        # "1_integer": [None],
        # "2_read": [1024, 1024 * 1024],
        # "3_write": [1024, 1024 * 1024],
        "4_movingaverage": [10],
    }

    threads = [1]
    # load_requests = 1
    load_threads = [10]  # [16]  # [1, 16]
    load_frequency = [1]
    delay_ms = 25  # ms, one-way
    bandwidth_mbps = 100  # Mbps
    delay_ms_client_edge = 1  # ms, one-way
    bandwidth_mbps_client_edge = 10  # Mbps
    deployment_modes = ["edge", "cloud"]
    repeats = [1, 2, 3]

    results_dir = "results/results-simple"
    os.makedirs(results_dir, exist_ok=True)

    output_dir = "output/output-simple"
    os.makedirs(output_dir, exist_ok=True)

    preparation_file = os.path.join(output_dir, "preparation.txt")

    experiments = []

    for f in functions:
        for p in functions[f]:
            for t in threads:
                for lt in load_threads:
                    for lf in load_frequency:
                        for m in deployment_modes:
                            for r in repeats:
                                # experiment should run about 5 minutes
                                load_requests = (5 * 60) * lf
                                # load_requests = 30 * lf
                                timeout = 6 * 60

                                e = Experiment(
                                    function=f,
                                    parameter=str(p),
                                    threads=t,
                                    load_requests=load_requests,
                                    load_threads=lt,
                                    load_frequency=lf,
                                    delay_ms=delay_ms,
                                    bandwidth_mbps=bandwidth_mbps,
                                    delay_ms_client_edge=delay_ms_client_edge,
                                    bandwidth_mbps_client_edge=bandwidth_mbps_client_edge,
                                    deployment_mode=m,
                                    timeout=timeout,
                                    repeat=r,
                                    results_dir=results_dir,
                                    output_dir=output_dir,
                                )
                                experiments.append(e)

    # randomize the order of experiments
    random.seed(0)
    random.shuffle(experiments)

    # prepare
    with open(preparation_file, "w") as of:
        print("Preparing...")
        print(f"Output: {preparation_file}")
        subprocess.run(["./prep-simple.sh"], stdout=of, stderr=of)

    try:
        for e in experiments:
            e.run()
    except Exception as e:
        print(e)
    except KeyboardInterrupt:
        print("KeyboardInterrupt")

    with open(preparation_file, "a") as of:
        print("Cleaning up...")
        print(f"Output: {preparation_file}")
        subprocess.run(["terraform", "destroy", "-auto-approve"], stdout=of, stderr=of)
