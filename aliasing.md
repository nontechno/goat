# Aliases

goat shows a few names around your windows: the host a window's program
runs on, its current directory, and the git repository and branch it is in.
Real names can be long or unhelpful (`build-server-07.us-west-2.corp.example.com`,
`/home/opc/src/github.com/nontechno/goat`). An **alias** is the name you'd
rather see instead.

Aliases live in one file:

```
~/.config/goat/alias.toml
```

(or `$XDG_CONFIG_HOME/goat/alias.toml`). It is optional: without it, goat
shows the real names.

## An example

```toml
# ~/.config/goat/alias.toml

[host]
"build-server-07.us-west-2.corp.example.com" = "build"
"*.corp.example.com" = "corp"

[directory]
"/home/opc" = "🏠"
"~/src/github.com" = "gh"

[repo]
"nontechno/goat" = "🐐"
"aaaaaaaaaaaa" = "aa"

[branch]
"*/main" = "🏡"
"aaaaaaaaaaaa/fdfsfsdfsdfs" = "monday"
```

With it, a window in `/home/opc/projects/goat` on the build server, in the
`nontechno/goat` repository on its `main` branch, has this bottom border:

```
└─ 🧑opc 💻build 📖🐐 🌿🏡 ─────────────────[]─┘
```

and the status bar shows its directory as `🏠/projects/goat`.

## How names are matched

Each line is `"real name" = "what to show"`. The left side is the **key**,
the right side the **alias**.

- **Exact keys first.** A key without `*` or `?` must match the whole name.
  If an exact key matches, it is used.
- **Then patterns.** In a key, `*` stands for any characters (including `/`)
  and `?` for exactly one character. Patterns are tried in the order they
  appear in the file; the first that matches is used.
- **No match, no change.** A name no key matches is shown as it is.
- **Real names only.** Keys always use the real names, never other aliases.
  A `[branch]` key names the real repository, not its alias.

## The sections

### `[host]`: host names

Applies to every host name goat shows:

- goat's own host, in window borders and at the right end of the status bar;
- the host an `ssh` session in a window logs in to, in that window's border;
- host names in the events window (`alice@build → root@build`).

Host names are matched **regardless of case**: `"BUILD-07"` matches `build-07`.

```toml
[host]
"build-07.corp.example.com" = "build"   # one machine
"*.corp.example.com" = "corp"           # every other machine there
"*.local" = "lan"
```

> This replaces the old `hostname = "..."` setting in `local.toml`, which is
> no longer read. To give your own machine a friendlier name, add its real
> name (what `hostname` prints) here.

### `[directory]`: directories

Applies to the directory shown in the status bar (`status_show_dir`).

A directory key is a **prefix**: it matches that directory **and everything
under it**, and only its own part of the path is replaced; the rest of the
path is kept as it is. Matching is by whole folder names, so a key never
matches part of a name.

When several keys match, the **longest** (most specific) one wins, wherever
the keys are in the file.

For example, with

```toml
[directory]
"/user/opc/home" = "🏠"
"/user/opc/home/abroad/villa" = "castle"
```

| Directory                          | Shown as               | Why                                       |
|------------------------------------|------------------------|-------------------------------------------|
| `/user`                            | `/user`                | above every key: unchanged                |
| `/user/opc`                        | `/user/opc`            | above every key: unchanged                |
| `/user/opc/home`                   | `🏠`                   | the key itself                            |
| `/user/opc/homestread`             | `/user/opc/homestread` | a different folder (`homestread` ≠ `home`) |
| `/user/opc/home/garage`            | `🏠/garage`            | under `/user/opc/home`                    |
| `/user/opc/home/abroad`            | `🏠/abroad`            | under `/user/opc/home` only               |
| `/user/opc/home/abroad/villa`      | `castle`               | both keys match: the longer one wins      |
| `/user/opc/home/abroad/villa/more` | `castle/more`          | both keys match: the longer one wins      |

More about directory keys:

- A key may start with `~` for your home folder: `"~/src/github.com" = "gh"`.
- A trailing `/` makes no difference: `"/user/opc/home/"` is the same key.
- A key with `*` or `?` is a pattern for the **whole** path, and the whole
  path is replaced: `"/srv/*/logs" = "logs"` shows `/srv/app/logs` as
  `logs` (but not `/srv/app/logs/old`). Patterns are used only when no
  prefix key matches.
- A directory no key covers is shown as before, with `~` for your home
  folder.

### `[repo]`: git repositories

Applies to the repository in window borders (`show_git`) and in
branch-switch events.

goat knows a repository by its real `owner/name`, from its `origin` remote
(for example `nontechno/goat` from `git@github.com:nontechno/goat.git`). A
repository without an owner (a local remote, or no remote at all) is just
`name`.

A key can be the full `owner/name`, or **just the name**:

```toml
[repo]
"nontechno/goat" = "🐐"       # this repository only
"aaaaaaaaaaaa" = "aa"         # any repository named aaaaaaaaaaaa
"acme/*" = "acme"             # every repository of acme
```

The full name is tried first, then the name alone.

### `[branch]`: git branches

Applies to the branch in window borders and in branch-switch events
(`goat: main → feature`).

A key is **`repository/branch`**, or **just the branch**. goat tries, in
order:

1. `owner/name/branch` (for example `nontechno/goat/main`);
2. `name/branch` (`goat/main`);
3. `branch` alone (`main`).

```toml
[branch]
"*/main" = "🏡"                         # main, in every repository
"aaaaaaaaaaaa/fdfsfsdfsdfs" = "monday"  # one branch of one repository
"release/*" = "rel"                     # release/1.0, release/2.0, … anywhere
```

Branch names can contain `/` (`feature/login`); that's fine, since `*`
matches `/` too.

## What an alias changes, and what it doesn't

Aliases are only **shown**: in window borders, the status bar and the events
window. Everything else still uses the real names. New windows start in the
real directory, ssh still connects to the real host, and the log file
records real names.

An alias may be text or emoji, up to 32 characters. Control characters and
invisible characters are removed, so an alias can't disturb the screen. An
empty alias (`"x" = ""`) is ignored.

## Editing the file

- goat checks the file every few seconds and picks up changes on its own;
  you don't need to restart it.
- If the file has a mistake (an unknown section such as `[hosts]`, a value
  that isn't text, broken TOML), goat lists it once in the events window
  (Alt+-) and uses **no** aliases until it is fixed, so you never see half of
  a file applied. Deleting the file turns all aliases off.
- TOML needs quotes around keys that contain anything but letters, digits,
  `-` and `_`. Quote every key and you won't have to think about it:
  `"/home/opc" = "🏠"`.
- Within a section, a key may appear only once (TOML's rule).

## Quick reference

| Section       | Key is                                  | Matches                      | Replaces             |
|---------------|-----------------------------------------|------------------------------|----------------------|
| `[host]`      | a host name (any case)                  | the whole name               | the whole name       |
| `[directory]` | a path (may start with `~`)             | the path and everything under it (longest key wins) | the matched part |
| `[repo]`      | `owner/name`, or `name`                 | the whole name               | the whole name       |
| `[branch]`    | `owner/name/branch`, `name/branch`, or `branch` | the whole key        | the branch           |

Everywhere: an exact key wins over patterns; `*` is any characters (`/`
included), `?` is one character; patterns are tried top to bottom.
