# dirextalk-connect

Bridge local AI coding agents to a Dirextalk Matrix agents room.

Chat with your AI dev assistant from anywhere.

## Install

```bash
npm install -g dirextalk-connect
```

## Usage

```bash
# Create config
dirextalk-connect --version

# Edit config.toml, then run
dirextalk-connect
dirextalk-connect -config /path/to/config.toml

# Optional daemon service
dirextalk-connect daemon install --config /path/to/config.toml --force

# Use one service name per Dirextalk node on the same machine
dirextalk-connect daemon install --config /path/to/t1/config.toml --service-name t1.dirextalk.ai --force
```

## Documentation

See full documentation at: https://github.com/YingSuiAI/dirextalk-connect
