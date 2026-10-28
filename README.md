# DevContainer Kit

[日本語](./README.ja.md)

**Build, maintain, and share your own Dev Container templates.**

DevContainer Kit is a forkable home for generating, maintaining, and distributing Dev Container templates with [codespacegen](https://github.com/taka1156/codespacegen), GitHub Actions, and a simple setup script. Use the published templates as-is, or fork the repository to build your own collection. Templates work with local VS Code Dev Containers and GitHub Codespaces.

**You do not need to install `codespacegen` to use a template.** Run the setup script hosted in this repository, select a language, and copy the generated configuration into your project.

## Two ways to use DevContainer Kit

- **Use a template:** Run the setup script and select a language. No `codespacegen` installation is needed.
- **Build your own collection:** Fork this repository, customize the generator configuration and workflows, and publish your own templates to a distribution branch.

## Quick start

Requirements: Bash, Git, an interactive terminal, and a Dev Container-compatible development environment.

Run this from your project directory:

```bash
curl -fsSL https://raw.githubusercontent.com/taka1156/codespace-template/master/scripts/setup_devcontainer.sh | bash
```

Or select a language by name:

```bash
curl -fsSL https://raw.githubusercontent.com/taka1156/codespace-template/master/scripts/setup_devcontainer.sh | bash -s -- node
```

> The script path above assumes `scripts/setup_devcontainer.sh` is committed to the `master` branch. Adjust it if you place the file elsewhere. Review remote scripts before executing them.

The script:
1. Retrieves `sample_devcontainer/` from the repository's `dist` branch using Git sparse checkout.
2. Discovers available languages from its subdirectories at runtime.
3. Prompts for a language (unless provided as an argument) and a project name.
4. Copies the selected template and updates the first `name` property in each `devcontainer.json` found.
5. Asks for confirmation if the destination already contains `.devcontainer`.

The default destination is the current directory. To use another directory:

```bash
curl -fsSL https://raw.githubusercontent.com/taka1156/codespace-template/master/scripts/setup_devcontainer.sh | DEST_DIR=./my-project bash
```

The script requires `/dev/tty` even when a language is passed, because it prompts for a project name. It does not silently overwrite an existing `.devcontainer`.

## Create your own Dev Container collection

1. **Fork** this repository into your GitHub account.
2. **Customize** the `codespacegen` configuration and generated language templates to fit your stack.
3. **Review and configure GitHub Actions** for template generation, publication, and periodic base-image version checks. Enable the workflows and grant only the permissions they need.
4. **Publish** the generated templates to your fork’s `dist` branch (or update the script if you choose a different branch).
5. **Point the installer at your fork:** update the script’s repository URL and branch settings, and use the raw script URL from your fork.
6. **Share** the installer command with your team, or use it yourself.

The fork is independent: it can manage different languages, image versions, and templates. Changes to your fork do not update the original repository.

## How it works

```text
DevContainer Kit (source branch: master)
├── codespacegen.json            Configuration for template generation
├── .github/workflows/           GitHub Actions workflows
└── scripts/
    └── setup_devcontainer.sh    Installer used by developers
          |
          | GitHub Actions / codespacegen
          v
DevContainer Kit (distribution branch: dist)
└── sample_devcontainer/
    ├── <language-a>/
    ├── <language-b>/
    └── ...
          |
          | Git sparse checkout + interactive selection
          v
Your project
└── .devcontainer/
    ├── devcontainer.json
    └── ... other generated files
```

**Roles:** `codespacegen` generates configurations; DevContainer Kit maintains and distributes them; the setup script installs selected templates into projects.

`codespacegen` is responsible for generating Dev Container configurations, while this repository distributes ready-made templates. GitHub Actions automates template generation/publication and periodically checks base-image versions for updates. The exact schedule and update behavior are defined by the workflow files.

## Updating templates

Templates are snapshots: running the setup script retrieves the version currently published on `dist`. Previously copied files in your project are **not automatically updated**. Review changes before running the installer again, especially if your project has customized `.devcontainer` files.

## Development environments

After installation, open your project with VS Code and a Dev Containers-compatible setup, or use GitHub Codespaces. For local use, Docker or another supported container runtime is required.

## Repository naming

The commands above currently use the existing `taka1156/codespace-template` repository URL. **DevContainer Kit** is the project name; if the GitHub repository is renamed, update the URLs and script defaults accordingly.

## Related project

- [codespacegen](https://github.com/taka1156/codespacegen) — CLI for generating Dev Container configuration from JSON.
