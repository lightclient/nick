# `nick`

`nick` is a vanity address searcher for deployments using [Nick's method][nm].

## Quick Start

```
go install github.com/lightclient/nick@latest
nick search --initcode="0x60425000"
```

## Usage

```
NAME:
   nick search - Search for a vanity address to deploy a contract using nicks method.

USAGE:
   nick search [command [command options]]

OPTIONS:
   --threads value   number of threads to search on (default: 10)
   --score value     minimum score number to report (default: 5)
   --prefix value    desired prefix in vanity address (default: "0x0000")
   --suffix value    desired suffix in vanity address (default: "0xaaaa")
   --initcode value  desired initcode to deploy at vanity address (default: "0x")
   --gaslimit value  desired gas limit for deployment transaction (default: 250000)
   --gasprice value  desired gas price (gwei) for deployment transaction (default: 1000)
   --help, -h        show help (default: false)
```

[nm]: https://yamenmerhi.medium.com/nicks-method-ethereum-keyless-execution-168a6659479c
