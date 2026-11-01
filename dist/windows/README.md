# Prebuilt Windows worker

`hyperpod-worker.exe` for x86_64 Windows, cross-compiled from Linux with the
`x86_64-pc-windows-gnu` target. It is committed so a Windows laptop can join the
pool with `git pull` alone, without installing Rust.

## Use

The launcher looks for the worker under `worker/target/release/`, so copy it
there after pulling:

```cmd
mkdir worker\target\release
copy dist\windows\hyperpod-worker.exe worker\target\release\
launchers\start-worker.bat
```

## Verify

```cmd
certutil -hashfile dist\windows\hyperpod-worker.exe SHA256
```

Compare against `hyperpod-worker.exe.sha256`.

## Caveat

A committed binary can fall behind the source. It is rebuilt only when the
worker changes and someone remembers to. If the two disagree, the source in
`worker/src/` is authoritative; rebuild with:

```sh
cargo build --release --target x86_64-pc-windows-gnu
```

This build corresponds to the commit that last touched `dist/windows/`.
