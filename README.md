[![main](https://github.com/flowerinthenight/g-ssh-cmd/actions/workflows/main.yml/badge.svg)](https://github.com/flowerinthenight/g-ssh-cmd/actions/workflows/main.yml)

A simple wrapper to `ssh -i key ec2-user@ip -t command` for AWS AutoScaling Groups. It uses your environement's `aws` command, as well as your SSH setup behind the scenes.

To install using [Homebrew](https://brew.sh/):

``` sh
$ brew install flowerinthenight/tap/g-ssh-cmd
```

Basic usage looks something like:

``` sh
$ g-ssh-cmd my-autoscaling-group 'uptime' --id-file ~/.ssh/key.pem
```
