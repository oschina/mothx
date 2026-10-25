# Skills System

MothX's Skills system allows you to create reusable prompt snippets called Skills.

## Overview

Skills are prompt snippets stored as `SKILL.md` files that can be loaded and injected into the system prompt.

```
┌─────────────────────────────────────────────────────────────┐
│                     Skills System                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Global Skills                 Project Skills                │
│  Linux/macOS: ~/.mothx/        .skills/                      │
│  skills/                                                       │
│  Windows: %APPDATA%\mothx\skills\                            │
│  ┌─────────────────────┐      ┌─────────────────────┐       │
│  │ coding-standards/   │      │ project-specific/   │       │
│  │   SKILL.md          │      │   SKILL.md          │       │
│  │                     │      │                     │       │
│  │ git-workflow/       │      │ testing-rules/      │       │
│  │   SKILL.md          │      │   SKILL.md          │       │
│  └─────────────────────┘      └─────────────────────┘       │
│            │                            │                    │
│            └──────────┬─────────────────┘                    │
│                       │                                      │
│                       ▼                                      │
│              ┌─────────────────┐                             │
│              │  System Prompt  │                             │
│              └─────────────────┘                             │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

## Skill Directories

### Global Skills

Location:
- Linux/macOS: `~/.mothx/skills/`
- Windows: `%APPDATA%\mothx\skills\`

Global skills are available for all projects.

```bash
# Linux/macOS
~/.mothx/skills/
├── coding-standards/
│   └── SKILL.md
├── git-workflow/
│   └── SKILL.md
└── review-checklist/
    └── SKILL.md

# Windows
%APPDATA%\mothx\skills\
├── coding-standards\
│   └── SKILL.md
├── git-workflow\
│   └── SKILL.md
└── review-checklist\
    └── SKILL.md
```

### Project Skills

MothX discovers project-local skills from these directories, in increasing priority order:

1. `.mothx/skills/` (the project config skills directory)
2. `.skills/`
3. `.agents/skills/`
4. `skills/`

Later directories override an earlier skill with the same name. Global skills are loaded first, so any project-local copy takes precedence. Each skill is a subdirectory containing `SKILL.md` or `skill.md`.

## SKILL.md Format

Each skill is a Markdown file containing the skill's description and content.

### Example

```markdown
# Coding Standards

This skill defines coding standards for the project.

## Go Code Style

- Use `gofmt` for formatting
- Follow Effective Go guidelines
- Use meaningful variable names
- Add comments for exported functions

## Error Handling

- Always handle errors
- Use `fmt.Errorf` with `%w` for wrapping
- Don't panic in library code

## Testing

- Write table-driven tests
- Use `t.Run` for subtests
- Aim for >80% coverage
```

### Structure

1. **Title** (H1): Skill name
2. **Description** (first paragraph): Brief description of the skill
3. **Content**: Detailed content and rules

## Configuration

### Skills Directory

Configure the skills directory in `settings.json`:

```json
{
  "skillsDir": "~/.mothx/skills"
}
```

On Windows, use `%APPDATA%\mothx\skills` or an absolute path.
```

### Project Local Skills

Project skills are discovered automatically from the project skill directories listed above, with no additional configuration required. Discovery/installation is separate from activation: the Web UI can browse/search SkillHub or ClawHub, install skills, and select which skills are active for a session.

## Usage

### Loading Skills

Skills are loaded automatically at startup:

```go
skillsMgr := skills.NewManager(globalSkillsDir, projectSkillsDir)
if err := skillsMgr.Load(); err != nil {
    // handle error
}
```

### Viewing Loaded Skills

Use the `/skills` command in interactive mode:

```
> /skills
Loaded 3 skills:
  - coding-standards (global)
  - git-workflow (global)
  - project-conventions (project)
```

### Skill Injection

Skill content is injected into the system prompt for the skills active in the current session. A discovered or installed skill is not necessarily enabled for every session.

## Best Practices

### 1. Keep Skills Concise

Focus each skill on a single topic:

```markdown
# Git Commit Rules

## Commit Message Format

<type>(<scope>): <subject>

## Types

- feat: New feature
- fix: Bug fix
- docs: Documentation
- style: Formatting
- refactor: Code restructuring
- test: Adding tests
- chore: Maintenance
```

### 2. Use Global Skills for Common Rules

Place rules that apply to all projects in the global skills directory:

```bash
# Linux/macOS
~/.mothx/skills/
└── general-rules/
    └── SKILL.md

# Windows
%APPDATA%\mothx\skills\
└── general-rules\
    └── SKILL.md
```

### 3. Use Project Skills to Override Specific Rules

Project skills can override global skills:

```bash
.skills/
└── general-rules/  # Overrides global general-rules
    └── SKILL.md
```

### 4. Organize Skill Directories

Organize skills by topic:

```bash
# Linux/macOS
~/.mothx/skills/
├── coding/
│   ├── go-style/
│   │   └── SKILL.md
│   └── python-style/
│       └── SKILL.md
├── git/
│   ├── commit-rules/
│   │   └── SKILL.md
│   └── branch-strategy/
│       └── SKILL.md
└── review/
    └── checklist/
        └── SKILL.md

# Windows
%APPDATA%\mothx\skills\
├── coding\
│   ├── go-style\
│   │   └── SKILL.md
│   └── python-style\
│       └── SKILL.md
├── git\
│   ├── commit-rules\
│   │   └── SKILL.md
│   └── branch-strategy\
│       └── SKILL.md
└── review\
    └── checklist\
        └── SKILL.md
```

## Example Skills

### Code Review Skill

```markdown
# Code Review Checklist

## Before Review

- [ ] Code compiles without errors
- [ ] Tests pass
- [ ] No lint warnings

## Review Points

- [ ] Clear variable/function names
- [ ] Proper error handling
- [ ] No hardcoded values
- [ ] Comments for complex logic
- [ ] Test coverage adequate

## Security

- [ ] No SQL injection
- [ ] No XSS vulnerabilities
- [ ] Proper input validation
- [ ] Secrets not exposed
```

### Testing Skill

```markdown
# Testing Guidelines

## Test Structure

```go
func TestFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    interface{}
        expected interface{}
    }{
        // test cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test logic
        })
    }
}
```

## Coverage

- Aim for >80% code coverage
- Focus on edge cases
- Test error paths
```

## Troubleshooting

### Skills Not Loading

1. Check directory permissions
2. Confirm `SKILL.md` filename is correct
3. Use `/skills` command to view loading status

### Skills Not Working

1. Confirm skill content format is correct
2. Check if system prompt contains skill content
3. Restart MothX to reload

## Related Documents

- [Configuration](configuration.md) - Skills directory configuration
- [Architecture](architecture.md) - Skills system architecture
- [Workflow Mode](workflow.md) - JavaScript DSL workflow orchestration

## Built-in Skills

### workflow-javascript Skill

When you enable `--workflows` mode, MothX automatically creates a `.skills/workflow-javascript/` directory under your project root with complete syntax rules, pattern skeletons, and best practices. The skill includes 8 reference files:

- Core rules (loaded by default)
- Research and Investigation patterns
- Serial and Parallel Composition
- Decision Routing
- Bounded While Loops
- Horizontal Multi-Agent Collaboration
- Master-Slave Small Teams
- Evaluator-Optimizer Review Passes
- Governance and Human Checkpoints

See the [Workflow Mode](workflow.md) documentation for details.
