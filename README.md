# naquadah

A lightweight Starlink gRPC simulator.

- Default port: 9200
- Config file: `naquadah.yaml`

## Usage

- Generate a default config YAML:

```
naquadah -gen-config -config naquadah.yaml
```

- Run the server:

```
naquadah -config naquadah.yaml -port 9200
```

Flags:
- `-port` gRPC port (default 9200)
- `-seed` random seed
- `-noisy` log requests
- `-events` emit background stream events
- `-config` path to YAML device config
- `-gen-config` write default config then exit

