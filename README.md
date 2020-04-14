# kraken

🐙 private and efficient audio RTC network.

## Architecture

monitor daemon manage all engine instances by system load.

engine listen VPC internal IP, no public IP, no load balancer.

client join room by calling monitor RPC to ensure room sticky.

monitor RPC proxy client requests to engine RPC with room sticky.

coturn listen VPC internal IP, serve with UDP load balancer public IP.
