# ndmcli

Read a [Neat Download Manager](https://www.neatdownloadmanager.com/) database and turn a saved job into a curl or aria2c command, or download the file from this process.

macOS keeps the database at `~/Library/Application Support/com.NeatDownloadManager/NeatDB.db`. The URL, method, file name, and user agent live in `downloads`. Each request header, including `Cookie`, is one row in `headers`. A partial multi-connection job also leaves `<id>/segments.bin` and `<id>/seg.xN` beside the database.

`list` never prints cookie values. `show` redacts `Cookie`, `Authorization`, and the POST body unless you pass `--reveal`. `curl` and `export` print those values, so treat that output as a secret.

## Install

Go 1.23 or newer.

```sh
go install github.com/sarkrui/NDMCli/cmd/ndmcli@latest
```

Or from a checkout:

```sh
go build -o ndmcli ./cmd/ndmcli
```

On Linux or Windows, pass `--db` or set `NDM_DB`. The default path exists only on macOS.

## Usage

```
ndmcli list [--status open|all|paused|error|complete] [--cookie] [--name TEXT] [--json]
ndmcli show <id> [--reveal] [--json]
ndmcli curl <id> [--resume] [-o FILE]
ndmcli export [<id>...] [--tool curl|aria2c|both] [--save FILE] [-n N] [-o NAME]
ndmcli download <id> [-o FILE] [-n N] [--resume] [--adopt] [--force]
```

`export` writes a command you can run on another machine. File names are relative, so the file lands in the directory where you run it. `--tool curl` and `--tool aria2c` print one command per job. `--save` writes a script with mode 0600. `--tool both` is that script and asks for curl or aria2c when you run it. aria2c cannot send a POST body; those jobs need `--tool curl`.

`download --resume` continues from the bytes already in the output file. `--adopt` copies Neat's `seg.xN` pieces into the output and downloads the gaps. Pause the job in Neat Download Manager before `--adopt`. `-n` splits a range-capable file across up to 8 connections.

`hls` and `media` rows point at a playlist. `curl` saves that URL as-is. `download` refuses them unless you pass `--force`.

```sh
ndmcli list --status paused --cookie
ndmcli show 42
ndmcli curl 42 | sh
ndmcli export 42 --tool aria2c
ndmcli export 42 --save jobs.sh && sh jobs.sh aria2c
ndmcli download 42 -o ~/Downloads/file.bin
ndmcli download 42 --adopt -n 8
```

The database is opened read-only. `download` closes it before the transfer so Neat Download Manager can keep writing.
