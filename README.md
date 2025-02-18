[![main](https://github.com/flowerinthenight/g-ssh-cmd/actions/workflows/main.yml/badge.svg)](https://github.com/flowerinthenight/g-ssh-cmd/actions/workflows/main.yml)

A simple wrapper to `ssh user@host -t command` for AWS [AutoScaling Groups](https://docs.aws.amazon.com/autoscaling/ec2/userguide/auto-scaling-groups.html) and GCP [Managed Instance Groups](https://cloud.google.com/compute/docs/instance-groups#managed_instance_groups). It uses your environment's `aws` and `gcloud` commands, as well as your SSH setup behind the scenes. This tool has been created primarily for tailing logs from multiple, managed VMs, without going through either CloudWatch (AWS) or Cloud Logging (GCP).

To install using [Homebrew](https://brew.sh/):

``` sh
$ brew install flowerinthenight/tap/g-ssh-cmd
```

Basic usage looks something like:

``` sh
# Tail AWS ASG VMs' system logs:
$ g-ssh-cmd my-autoscaling-group 'journalctl -f' --id-file ~/.ssh/key.pem

# Tail GCP MIG VMs system logs:
$ g-ssh-cmd --vendor=gcp my-mig 'journalctl -f'
```
