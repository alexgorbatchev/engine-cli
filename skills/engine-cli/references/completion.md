# Engine CLI completion command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli completion`

Generate the autocompletion script for the specified shell

Usage: `engine-cli completion [flags]`.

Generate the autocompletion script for engine-cli for the specified shell.
See each sub-command's help for details on how to use the generated script.


- `--help, -h` (bool; default `false`): help for completion.

## `engine-cli completion bash`

Generate the autocompletion script for bash

Usage: `engine-cli completion bash`.

Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(engine-cli completion bash)

To load completions for every new session, execute once:

#### Linux:

	engine-cli completion bash > /etc/bash_completion.d/engine-cli

#### macOS:

	engine-cli completion bash > $(brew --prefix)/etc/bash_completion.d/engine-cli

You will need to start a new shell for this setup to take effect.


- `--help, -h` (bool; default `false`): help for bash.
- `--no-descriptions` (bool; default `false`): disable completion descriptions.

## `engine-cli completion fish`

Generate the autocompletion script for fish

Usage: `engine-cli completion fish [flags]`.

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	engine-cli completion fish | source

To load completions for every new session, execute once:

	engine-cli completion fish > ~/.config/fish/completions/engine-cli.fish

You will need to start a new shell for this setup to take effect.


- `--help, -h` (bool; default `false`): help for fish.
- `--no-descriptions` (bool; default `false`): disable completion descriptions.

## `engine-cli completion powershell`

Generate the autocompletion script for powershell

Usage: `engine-cli completion powershell [flags]`.

Generate the autocompletion script for powershell.

To load completions in your current shell session:

	engine-cli completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.


- `--help, -h` (bool; default `false`): help for powershell.
- `--no-descriptions` (bool; default `false`): disable completion descriptions.

## `engine-cli completion zsh`

Generate the autocompletion script for zsh

Usage: `engine-cli completion zsh [flags]`.

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(engine-cli completion zsh)

To load completions for every new session, execute once:

#### Linux:

	engine-cli completion zsh > "${fpath[1]}/_engine-cli"

#### macOS:

	engine-cli completion zsh > $(brew --prefix)/share/zsh/site-functions/_engine-cli

You will need to start a new shell for this setup to take effect.


- `--help, -h` (bool; default `false`): help for zsh.
- `--no-descriptions` (bool; default `false`): disable completion descriptions.
