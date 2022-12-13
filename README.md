# terragrunt-gitlabci-config

Heavily inspired on [Terragrunt Atlantis Configuration](https://github.com/transcend-io/terragrunt-atlantis-config) - This will read a [Terragrunt-like directory layout](https://github.com/gruntwork-io/terragrunt-infrastructure-live-example), interpret all `local` Terragrunt config blocks while parsing an input TXT Golang template that should effectively mimic a `.gitlab-ci.yml` file.

> The actual input file should be a valid `.gitlab-ci.yml` file with Golang. An example can be found [here](test/inputs/.gitlab-ci.yml.tpl)

```bash
> terragrunt-gitlabci-config --help
By taking a Golang TXT template file, it generates GitlabCI Config for Terragrunt IaC live style projects maintained in a mono-repo fashion.

Usage:
  terragrunt-gitlabci-config [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  generate    Creates GitLab CICD Dynamic configuration
  help        Help about any command
  version     Version of terragrunt-gitlabci-config

Flags:
  -h, --help               help for terragrunt-gitlabci-config
  -v, --verbosity string   Log level (debug, info, warn, error, fatal, panic (default "info")

Use "terragrunt-gitlabci-config [command] --help" for more information about a command.
```
<!-- TOC -->

- [terragrunt-gitlabci-config](#app)
  - [Get it](#get-it)
  - [Use it](#use-it)
    - [Examples](#examples)

<!-- /TOC -->

## Get it

Using go get:

```bash
go get -u github.com/kitos9112/terragrunt-gitlabci-config
```

Or [download the binary](https://github.com/kitos9112/terragrunt-gitlabci-config/releases/latest) from the releases page.

```bash
# Linux x86_64
curl -L https://github.com/kitos9112/terragrunt-gitlabci-config/releases/download/1.4.0/terragrunt-gitlabci-config_1.4.0_linux_x86_64.tar.gz | tar xz
# Linux arm64
curl -L https://github.com/kitos9112/terragrunt-gitlabci-config/releases/download/1.4.0/terragrunt-gitlabci-config_1.4.0_linux_arm64.tar.gz | tar xz

# OS X x86_64
curl -L https://github.com/kitos9112/terragrunt-gitlabci-config/releases/download/1.4.0/terragrunt-gitlabci-config_1.4.0_osx_x86_64.tar.gz | tar xz
# OS X arm64
curl -L https://github.com/kitos9112/terragrunt-gitlabci-config/releases/download/1.4.0/terragrunt-gitlabci-config_1.4.0_osx_arm64.tar.gz | tar xz

# Windows x86_64
curl -LO https://github.com/kitos9112/terragrunt-gitlabci-config/releases/download/1.4.0/terragrunt-gitlabci-config_1.4.0_windows_x86_64.zip
unzip terragrunt-gitlabci-config_1.4.0_windows_x86_64.zip
```

## Use it

```text

terragrunt-gitlabci-config [OPTIONS] [COMMAND [ARGS...]]

/bin/sh: terragrunt-gitlabci-config: command not found
```

### Examples

WIP
