# Metrics

StatsD integration is enabled. Configure the endpoint in `config/config.toml`:

```toml
[metrics]
endpoint = "127.0.0.1:8125"
prefix   = "ffxiv-census"
```

Each HTTP request emits a timing metric named `http.<route>`. Extend the middleware to add counters or histograms as needed.
