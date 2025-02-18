[![main](https://github.com/flowerinthenight/g-ssh-cmd/actions/workflows/main.yml/badge.svg)](https://github.com/flowerinthenight/g-ssh-cmd/actions/workflows/main.yml)

A simple wrapper to `ssh user@host -t command` for AWS AutoScaling Groups and GCP Managed Instance Groups. It uses your environment's `aws` and `gcloud` commands, as well as your SSH setup behind the scenes. This tool has been created primarily for tailing logs from multiple, managed VMs, without going through CloudWatch (AWS) or Cloud Logging (GCP).

To install using [Homebrew](https://brew.sh/):

``` sh
$ brew install flowerinthenight/tap/g-ssh-cmd
```

Basic usage looks something like:

``` sh
$ g-ssh-cmd my-autoscaling-group 'journalctl -f' --id-file ~/.ssh/key.pem
```
