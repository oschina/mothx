# Online Skill Marketplace Integration

MothX is compatible with existing skill marketplaces (源盾 / SkillHub / ClawHub). Skill packages published on these platforms can be used directly in MothX.

| Platform | URL | Region |
|----------|-----|--------|
| **源盾 (Yuandun)** | [https://yuandun.gitee.com](https://yuandun.gitee.com/) | China |
| **SkillHub** | [https://skillhub.cn](https://skillhub.cn/) | China |
| **ClawHub** | [https://clawhub.ai](https://clawhub.ai/) | International |

MothX includes a built-in TUI marketplace for browsing, searching, inspecting, and
installing public 源盾 / SkillHub / ClawHub packages. Open it with `/skillhub`. 源盾 is
listed first and is the default market. MothX identifies itself to 源盾 with
`X-Client-Source: MothX` and `User-Agent: MothX`; 源盾 uses the header for download attribution.

This guide covers:

1. [Installing Skills from Marketplaces](#installing-skills-from-marketplaces) — TUI and command usage
2. [Skill Format Compatibility](#skill-format-compatibility) — standard format details
3. [Local Skill System](#local-skill-system) — built-in features
4. [Cron Foundation](#cron-foundation) — scheduled task infrastructure

---

## Installing Skills from Marketplaces

Open the built-in marketplace with `/skillhub`.

In the `mothx serve` Web UI, open **Skills** from the sidebar. The Web UI and TUI share
the same 源盾 / SkillHub / ClawHub adapters and safe installer, including Official, Browse,
Search, detail, install, update, and current-session activation. Project installs are
derived from the current session workDir and constrained by `allowedWorkDirs`.

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Switch 源盾 / SkillHub.cn / ClawHub.ai |
| `[` / `]` | Switch Browse / Search / Official |
| `/` | Edit the search query; `Enter` searches |
| `Up` / `Down` | Select a skill |
| `Left` / `Right` | Page through cursor-based markets (源盾 / ClawHub) or SkillHub pages |
| `Enter` | Load details and file list |
| `d` | Open the scrollable files, security, and evaluation detail view |
| `c` / `s` | Cycle category / sort in 源盾 or SkillHub.cn Browse |
| `i` | Install to the selected scope |
| `u` | Update an installed skill when a newer version is available |
| `a` | Install and activate for the current session |
| `g` / `p` | Select global / project scope |
| `r` | Refresh |
| `Esc` | Close the marketplace |

Lists show download counts as `DL`. SkillHub.cn Browse is ordered by downloads.
源盾 Browse uses the market's cursor and supports the same category and sort controls.
`[official]` means the skill is published by a configured Official account;
`[certified]` identifies a verified publisher; `[verified]` is the marketplace's
skill verification flag; and `[risk]` marks a suspicious result. These labels are
kept separate because account origin is not the same as platform verification.
Details also show source, category, tags, security scan summaries, evaluation availability, and download endpoints when provided by the market. The `d` view distinguishes primary and fallback download sources.
SkillHub.cn Browse supports category filters and descending downloads, stars, installs, score, or updated-time sorting. 源盾 Browse supports its fixed categories and COMPREHENSIVE / DOWNLOADS / STARS / LATEST sorting through the same controls.
Installed skills with a newer version show `[update]`; press `u` to update. Active skills are reloaded into the current session after an update.

The command form is also available:

```text
/skillhub search <query>
/skillhub detail <market>/<id>
/skillhub install <market>/<id> [--global|--project] [--activate]
/skillhub installed
```

Installation and activation are separate. Installation writes the package and refreshes
the local skill list. Activation also rebuilds the current session agent so the next
request includes the skill.

Downloaded archives are size-limited and checked for path traversal, absolute paths,
Windows drive paths, and symbolic links. Existing hand-written skills are not overwritten;
updates can only replace a managed directory installed from the same marketplace entry.

Manual installation remains supported by placing a compatible skill directory in one of
the local skill directories.

---

## Skill Format Compatibility

MothX's skill format is fully compatible with the 源盾 / SkillHub / ClawHub standard:

```
skill-name/
├── SKILL.md              # Required: skill definition
└── references/           # Optional: on-demand reference files
    ├── api-guide.md
    └── examples.md
```

### SKILL.md Standard Format

```markdown
# Skill Name

Short description.

## Rules

- Rule 1
- Rule 2

## Examples

...
```

### Reference Files

Skills can include reference files under a `references/` directory, loaded on demand via the `skill_ref` tool:

```
> skill_ref(skill="go-expert", ref="references/api-guide.md")
→ Returns the content of api-guide.md
```

This allows skills to include extensive reference material without consuming system prompt space.

---

## Local Skill System

In addition to marketplace downloads, you can create local skills directly.

### Skill Directories

| Type | Location | Scope |
|------|----------|-------|
| Global | `~/.mothx/skills/` (Linux/macOS) or `%APPDATA%\mothx\skills\` (Windows) | All projects |
| Project | `.mothx/skills/`, `.skills/`, or `skills/` | Current project, overrides global in that order |

### Creating a Skill

```bash
mkdir -p ~/.mothx/skills/go-expert
cat > ~/.mothx/skills/go-expert/SKILL.md << 'EOF'
# Go Expert

Expert-level Go coding standards.

## Rules

- Use `gofmt` for formatting
- Follow Effective Go guidelines
- Return errors; do not panic
- Use `fmt.Errorf` with `%w` for wrapping

## Testing

- Write table-driven tests
- Use `t.Run` for subtests
- Aim for >80% coverage
EOF
```

### Using Skills

```
> /skills
Loaded 2 skills:
  - go-expert (global)
  - project-conventions (project)

> /skill:go-expert
Loaded skill: go-expert
```

### Configuration

Configure the global skills directory in `settings.json`:

```json
{
  "skillsDir": "~/.mothx/skills",
  "skillHub": {
    "defaultMarket": "yuandun",
    "defaultInstallScope": "project",
    "officialHandles": ["user_0064faa7"],
    "markets": [
      { "id": "yuandun", "name": "源盾", "enabled": true, "apiURL": "https://yuandun.gitee.com" },
      { "id": "skillhub.cn", "name": "SkillHub.cn", "enabled": true },
      { "id": "clawhub.ai", "name": "ClawHub.ai", "enabled": true }
    ]
  }
}
```

`officialHandles` controls the accounts aggregated in the SkillHub.cn Official tab.
The 源盾 market ships with `https://yuandun.gitee.com` as its default `apiURL`;
override it only when using a mirror. 源盾 is ordered before SkillHub.cn and is the
default market.
Project skills load automatically without extra configuration.

---

## Cron Foundation

MothX has an internal cron infrastructure (`internal/cron` package) and TUI command entry points. The cron store persists session-bound jobs to the `cron_jobs` table in `sessions.db`, and the scheduler checks for due jobs on a 30-second interval.

### `/cron` TUI Commands

Requires multi-agent mode (`--multi-agent` or Ctrl+P to toggle):

```
> /cron add <description>      — Add a scheduled task
> /cron list                   — List scheduled tasks
> /cron enable <id>            — Enable a task
> /cron disable <id>           — Disable a task
> /cron remove <id>            — Remove a task
> /cron run <id>               — Run a task now
```

### Cron Job Data Model

| Field | Description |
|-------|-------------|
| `id` | Unique job ID (e.g. `cron-1716883200`) |
| `name` | Short task description |
| `prompt` | Task prompt for sub-agent |
| `schedule` | 5-field cron expression |
| `mode` | `agent` or `yolo` |
| `enabled` | Whether the job is active |
| `last_run` | Timestamp of last execution |
| `next_run` | Computed next execution time |
| `run_count` | Total executions |
| `last_status` | `success`, `failed`, or `running` |

### Scheduler Architecture

```
Scheduler loop (every 30s)
    │
    ├── List all enabled jobs from store
    │
    ├── Check each job: is it due?
    │   ├── Never run before → due
    │   ├── NextRun has passed → due
    │   └── Last run > 1 hour ago → due (fallback)
    │
    └── Due jobs → spawn sub-agent
              │
              ├── Mark job as "running"
              ├── Create agent via AgentManager
              ├── Run agent with job prompt
              ├── Collect result
              └── Update job status (success/failed)
```

---

## Related Documents

- [Skills System](skills.md) — Local skills format and management
- [Configuration](configuration.md) — Full settings reference
- [Security](security.md) — Sandbox and approval controls
