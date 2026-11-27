# mw-feed

[English](README.md)

Lecteur de flux auto-hébergé, mono-utilisateur, écrit en Go. Binaire unique, stockage SQLite, interface rendue côté serveur.

Statut : en développement, voir [docs/PLAN.md](docs/PLAN.md).

## Compilation

```sh
cd src
CGO_ENABLED=0 go build ./cmd/mw-feed
```

## Crédits

mw-feed est une réécriture indépendante du comportement de **KrISS feed**, application PHP créée par **Tontof** ([tontof.net](http://tontof.net), [github.com/tontof/kriss_feed](https://github.com/tontof/kriss_feed)). Aucun code ni asset du projet d'origine n'est repris.

## Licence

GNU General Public License v3.0 ou ultérieure. Voir [LICENSE](LICENSE) et [NOTICE](NOTICE).
