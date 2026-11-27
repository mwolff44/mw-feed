# mw-feed

A self-hosted, single-user feed reader written in Go. Single binary, SQLite storage, server-rendered interface.

Status: under development, see [docs/PLAN.md](docs/PLAN.md).

## Build

```sh
cd src
CGO_ENABLED=0 go build ./cmd/mw-feed
```

## Credits

mw-feed is an independent rewrite of the behaviour of **KrISS feed**, a PHP application created by **Tontof** ([tontof.net](http://tontof.net), [github.com/tontof/kriss_feed](https://github.com/tontof/kriss_feed)). No code or asset from the original project is reused.

## License

GNU General Public License v3.0 or later. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
