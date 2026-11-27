# Plan de réécriture de KrISS feed en Go

> Nom du projet : **mw-feed**. Module Go : `github.com/mwolff44/mw-feed`. Binaire : `mw-feed`. Ne pas réutiliser la marque « KrISS feed » dans le nom, l'interface ou la documentation, sauf pour le crédit (section 13).

Document destiné à un agent de développement. Il décrit **quoi** construire, les décisions déjà prises par le propriétaire du projet, et un découpage en phases vérifiables. Ce qui n'est pas décidé est listé en section 12 : **ne pas trancher soi-même, demander**.

Référence fonctionnelle : l'application PHP du dépôt `kriss_feed`, située **hors de ce dépôt** dans `../kriss_feed/` (`src/kriss_feed.php`, `src/class/*.php`, `src/class/tpl/*.html`, `src/inc/script.js`, `src/po/messages/`). Elle sert de spécification de comportement, pas de modèle d'architecture. Les chemins `src/...` cités dans ce document, hors section 2, désignent ce dépôt PHP.

**Règle de travail : ne rien extrapoler.** Si une information manque ou n'est pas claire, l'agent la demande au propriétaire au lieu de la deviner.

---

## 0. Décisions arrêtées (ne pas remettre en cause)

| # | Sujet | Décision |
|---|-------|----------|
| 1 | Migration | **Aucune.** Repartir de zéro. L'import OPML est le seul moyen de reprendre ses flux. |
| 2 | Stockage | **SQLite** embarqué, pilote pur Go `modernc.org/sqlite` (sans CGO). |
| 3 | Distribution | Binaire unique (templates, CSS, JS, traductions embarqués via `go:embed`) **+ Dockerfile** (image minimale). Pas de binaires multiplateformes. |
| 4 | Déploiement | Serveur HTTP simple, port configurable. Pas de TLS intégré, pas de sous-chemin : reverse proxy en frontal, application servie à la racine. |
| 5 | Mises à jour | **Ordonnanceur interne** en arrière-plan. Pas d'endpoint cron externe. Les mises à jour manuelles (flux, dossier, tout) restent dans l'interface. |
| 6 | Plugins | **Supprimés.** Aucune notion de hook ou de plugin. |
| 7 | Interface | **Modernisée** (nouveau design), rendue côté serveur avec `html/template`, CSS écrit à la main, **htmx** (embarqué, sans étape de build) plus un peu de JS maison. **Utilisable sans JavaScript.** Pas de SPA. Pas d'API JSON publique. |
| 7c | Thèmes | Clair et sombre. Réglage dans la configuration : automatique (`prefers-color-scheme`), clair, sombre. Les 11 thèmes actuels sont abandonnés. |
| 8 | Fonctionnalités supprimées | Partage Shaarli, protection de session par IP, ajout de flux par URL en paramètre (`?add&newfeed=`). |
| 8 | Fonctionnalités conservées | Anonymisation des liens (redirector), favicons, précharge/cache, réglages `blank`, `swipe`, `autofocus`. |
| 5b | Visibilité | **Privé uniquement** : tout exige une connexion. Pas de mode public ni protégé. |
| 9 | Authentification | Un seul utilisateur, mot de passe haché avec **argon2id** (`golang.org/x/crypto/argon2`, 19 MiB, t=2, p=1, format PHC), formulaire d'installation au premier lancement, session par cookie, CSRF, expiration par inactivité (3600 s), bannissement après 4 échecs pendant 1800 s, case « rester connecté », changement de mot de passe. Valeurs **fixes**. |
| 10 | i18n | Fichiers JSON embarqués, un par langue : **en** (défaut), **fr**, **de**. Langue réglée dans la configuration. |
| 11 | Outils | Go dernière stable (≥ 1.25) ; `net/http` (ServeMux, **ni Gin ni GORM** : SQL écrit à la main avec `database/sql`) ; `html/template` ; `modernc.org/sqlite` ; `golang.org/x/crypto` ; htmx ; `github.com/mmcdole/gofeed` ; OPML maison avec `encoding/xml` ; `github.com/microcosm-cc/bluemonday`. |
| 11 | Dépôt, licence | **Nouveau dépôt**, licence **GPL v3** (GPL-3.0-or-later, texte intégral dans `LICENSE`). |
| 12a | Crédit | Le projet est une réécriture de **KrISS feed**, créé par **Tontof** (http://tontof.net, https://github.com/tontof/kriss_feed). Le crédit est obligatoire (voir section 13). |
| 12b | Port | Port par défaut **8080**. |
| 12c | Reverse proxy | Option (désactivée par défaut) « faire confiance au proxy » : IP lue dans `X-Forwarded-For`, protocole dans `X-Forwarded-Proto`. Pas de liste de proxys. |
| 12d | « Rester connecté » | **30 jours**, renouvelés à chaque visite. |
| 12e | SSRF | Adresses loopback/privées/lien-local bloquées par défaut ; option (flag ou variable d'environnement) pour les autoriser. |
| 12f | Réglages supprimés | `autoUpdate`, `autoreadPage`, personnalisation de l'ordre du menu et de la pagination (ordre fixe). |
| 12g | Étoilés | Conservés quand leur flux est supprimé, avec le nom du flux d'origine mémorisé. |
| 12h | Favicon désactivé | Icône par défaut embarquée (aucune requête tierce). |
| 13 | Code source | Tout le code Go est dans `src/`, avec **`go.mod` dans `src/`**. `Dockerfile`, `.github/` et `docs/` restent à la racine. |
| 14 | Docker | Image finale `gcr.io/distroless/static:nonroot`. Pas de shell : `HEALTHCHECK` via la sous-commande `mw-feed healthcheck` qui interroge `/healthz`. |
| 15 | Configuration d'exécution | Flags ou variables d'environnement : `MW_FEED_ADDR` (défaut `:8080`), `MW_FEED_DATA`, `MW_FEED_TRUST_PROXY`, `MW_FEED_ALLOW_PRIVATE`. |
| 16 | Migrations | **Une migration par phase** (une table est créée quand sa phase en a besoin). Mécanisme maison : `PRAGMA user_version` + fichiers `.sql` embarqués, bibliothèque standard seule. Une base de version **plus récente** que le binaire est **refusée** avec une erreur explicite. |
| 17 | Réglages | `SaveSettings` **valide** les valeurs dans le store et renvoie une erreur sinon : `order` ∈ {newerFirst, olderFirst}, `view` ∈ {list, expanded}, `filter` ∈ {unread, all}, `theme` ∈ {auto, light, dark}, `locale` ∈ {en, fr, de}, `by_page`, `max_items`, `max_update` ≥ 1, sans maximum. |
| 18 | Configuration d'exécution | Défaut de `MW_FEED_DATA` : `./data` (l'image Docker fixe `/data`). Flags : `-addr`, `-data`, `-trust-proxy`, `-allow-private`. Priorité **flag > variable d'environnement > défaut**. Booléens via `strconv.ParseBool` ; valeur invalide = erreur et refus de démarrer. |
| 19 | SQLite | Répertoire de données créé par `os.MkdirAll` en `0o750`. Une seule connexion (`SetMaxOpenConns(1)`, lectures comprises). Pas de commande `serve` avant la phase 2. |
| 20 | i18n dès la phase 2 | Paquet `i18n` minimal (`T(locale, clé)`, `en.json` seul) utilisé par les pages dès la phase 2. Clé absente : repli sur l'anglais, puis la clé elle-même. Les traductions fr/de et le test de cohérence restent en phase 7. |
| 21 | Mot de passe (OWASP) | **15 à 128 caractères Unicode** (comptés en caractères, pas en octets), espaces autorisés, aucune règle de composition, pas de liste de mots de passe compromis, pas de normalisation NFKC. Double saisie à l'installation et au changement ; le changement exige le mot de passe actuel. Identifiant : non vide après suppression des espaces. Connexion : même erreur (et même temps de calcul) pour identifiant inconnu ou mot de passe faux. |
| 22 | CSRF avant connexion | Une **session anonyme** (table `sessions`, `authenticated = 0`) porte le jeton CSRF des formulaires d'installation et de connexion ; son identifiant est régénéré à la connexion. Les sessions anonymes expirent après 3600 s d'inactivité. Nettoyage **opportuniste** à chaque création de session (sessions expirées, bannissements terminés), sans tâche de fond. |
| 23 | Bannissement | Compte uniquement les échecs de connexion (pas ceux du changement de mot de passe), remis à zéro par une connexion réussie, sans décroissance dans le temps. 4 échecs → 1800 s ; pendant le bannissement toute tentative est refusée (429, durée restante affichée) sans le prolonger ; à son terme le compteur repart de 0. |
| 24 | Changement de mot de passe | Supprime toutes les autres sessions ; la session courante reçoit un nouvel identifiant et un nouveau jeton CSRF. |
| 25 | URLs et cookie | `GET/POST /install`, `GET/POST /login`, `POST /logout`, `GET/POST /password`, `GET /` (page d'accueil provisoire jusqu'à la phase 4). Cookie `mw_feed_session`. `/install` répond 404 une fois installé. Pas de paramètre `?next=`. |
| 26 | Commande `serve` | `mw-feed serve` démarre le serveur et s'arrête proprement sur `SIGTERM`/`SIGINT` ; `mw-feed` sans argument reste une erreur. |
| 27 | Périmètre phase 3 | Logique uniquement (téléchargement, parsing, assainissement, service de flux, dossiers), **sans page web** (les pages restent en phase 7). Pour tester la rétention, le service expose dès la phase 3 le strict minimum : marquer **un** élément lu/non lu, étoiler/désétoiler **un** élément, lister les éléments **d'un flux**. La phase 4 construit sélections, filtres, tris, pagination et marquages en masse par-dessus. |
| 28 | Éléments retenus | Les éléments du flux sont **triés par date décroissante** (tri stable) puis tronqués à `maxItems` (écart volontaire avec le PHP, qui garde l'ordre du flux). |
| 29 | Normalisation (reprise du PHP, adaptée à gofeed) | Titre vide → lien. Date : publication, sinon mise à jour, sinon date courante. `content` : contenu complet, sinon description/résumé. `description` : description/résumé, sinon contenu, balises retirées puis troncature à 500 caractères Unicode. Lien : lien de l'élément (Atom : `alternate`), sinon GUID **si URL http(s)**, sinon élément ignoré. Pièces jointes : `enclosure` et `media:content` ; miniature : `media:thumbnail` (rendu 4.4). Titre, auteur, description et titre du flux : **texte brut** (balises retirées, entités décodées, espaces normalisés, avant troncature). |
| 30 | Dossiers | Titre **unique**, comparé après suppression des espaces, **sensible à la casse**. Création ou renommage vers un titre existant : erreur explicite (pas de fusion). « Nouveau dossier » saisi à l'ajout ou à l'édition d'un flux avec un titre existant : le flux est ajouté au dossier existant. Titre vide refusé à la création ; en renommage, titre vide = suppression. Supprimer un dossier ne supprime pas ses flux. |
| 31 | Édition d'un flux | Titre obligatoire (non vide) ; description effaçable ; URL du site effaçable, http(s) si renseignée. URL XML modifiable : http(s) uniquement, unique parmi les flux, **aucun téléchargement** à la modification, validateurs HTTP effacés. Délai vide/`auto` → automatique ; nombre hors de [5, `maxUpdate`] → automatique sans erreur. La liste de dossiers envoyée remplace l'ancienne. Renommer un flux met à jour `feed_title` de tous ses éléments (pas `author`). |
| 32 | Ajout et mise à jour | Flux sans titre : **nom d'hôte** de l'URL XML. Une mise à jour ne modifie jamais le titre ; elle remplit description et URL du site **seulement si vides**. `last_update_at` est renseignée à chaque tentative, `last_error` effacée après un succès. |
| 33 | Avertissement | Colonne `feeds.last_warning` : **clé de traduction** (`items_missed`), indépendante de `last_error`, effacée à la mise à jour suivante où la règle 4.3-5 ne s'applique plus. |
| 34 | Requêtes conditionnelles | Colonnes `feeds.http_etag` et `feeds.http_last_modified`. Mise à jour **planifiée** : `If-None-Match` / `If-Modified-Since` ; réponse 304 = rien à analyser (`last_update_at` renseignée, `last_error` effacée, éléments et `last_warning` inchangés) ; 200 = analyse puis nouveaux validateurs. Mise à jour **manuelle** et ajout : téléchargement complet sans en-têtes conditionnels. Échec : validateurs inchangés. |
| 35 | Téléchargement | `User-Agent: mw-feed` (sans version ni URL), aucun autre en-tête. Statuts 2xx seulement ; au-delà de 10 Mo : erreur (pas de troncature) ; 10 redirections maximum, http(s) uniquement, contrôle SSRF à chaque étape ; délai total 30 s ; type de contenu non vérifié. |
| 36 | SSRF | Bloqués : loopback (`127.0.0.0/8`, `::1`), privées (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`), lien-local (`169.254/16`, `fe80::/10`), non spécifiée (`0.0.0.0`, `::`), multicast, CGNAT (`100.64/10`) ; IPv6 encapsulant une IPv4 normalisée avant contrôle ; contrôle sur l'**adresse réellement utilisée pour la connexion**. `MW_FEED_ALLOW_PRIVATE` lève toutes les restrictions d'adresse, pas celles de schéma, taille et redirections. |
| 37 | Assainissement | Une **seule** passe, **à la fin** : contenu du flux + pièces jointes (4.4), puis la politique unique, dérivée de la politique `bluemonday` pour contenu tiers. Autorisés : `img`, `audio`, `video`, `source` (sources http/https) et `iframe` vers `https://www.youtube-nocookie.com/` uniquement. Retirés : scripts, autres `iframe`, `on*`, `javascript:`, styles en ligne. |
| 38 | Tri des flux | Par titre, **tri naturel insensible à la casse** (comme `strnatcasecmp`), à égalité par identifiant. |
| 12 | Tests | **Bibliothèque standard uniquement** (aucune bibliothèque d'assertions ni de mocks, horloge injectable maison). Unitaires, intégration HTTP (`httptest`, SQLite temporaire, faux serveur de flux), `go vet` + `golangci-lint` + `go test -race`, CI GitHub Actions (tests + build de l'image Docker). Pas de tests de parité avec l'application PHP. |

---

## 1. Périmètre fonctionnel

### 1.1 Fonctionnalités à reproduire

1. **Installation** au premier lancement : formulaire (identifiant, mot de passe). Impossible de l'utiliser une fois installé.
2. **Connexion / déconnexion / changement de mot de passe**, avec bannissement après échecs.
3. **Flux** : ajout par URL (le formulaire demande l'URL, des dossiers existants à cocher et un nouveau dossier optionnel), édition (titre, URL XML, URL du site, description, dossiers, délai de mise à jour), suppression, affichage de la dernière mise à jour et de l'erreur éventuelle.
4. **Dossiers** : création, renommage, suppression (titre vide à l'édition), ordre, repli/dépli (`toggleFolder`), flux dans plusieurs dossiers possible (un flux a une liste de dossiers).
5. **Édition globale** : réordonner les dossiers, ajouter/retirer des dossiers à une sélection de flux, liste des flux avec accès à l'édition.
6. **Éléments (articles)** : liste, vue étendue, lecture, marquer lu/non lu (un élément, un flux, un dossier, tout), étoiler/désétoiler.
7. **Vues** : `list` (liste repliable) et `expanded` ; filtre `unread` / `all` ; ordre `newerFirst` / `olderFirst` ; liste des flux `show` / `hide` ; pagination (`byPage`, défaut 10) ; sélection courante (tout, un dossier, un flux).
8. **Étoilés** : page dédiée, mêmes sélections (tout, dossier, flux), conservés même si l'élément disparaît du flux source.
9. **Mises à jour** : par flux, par dossier, pour tout ; automatiques en arrière-plan (section 4.2).
10. **Import / export OPML** (dossiers imbriqués gérés, voir `src/class/Opml.php`).
11. **Configuration** : voir section 1.3.
12. **Aide** : page des raccourcis clavier et de la présentation.
13. **Raccourcis clavier** : voir section 6.3.
14. **Favicons** : icône de chaque flux à côté de son titre.
15. **Anonymisation des liens** : préfixe d'URL configurable, appliqué aux liens des articles (pas aux images ni médias). Valeur spéciale `noreferrer` pour utiliser l'attribut HTML5.
16. **Précharge** : charger en arrière-plan les éléments de la page courante pour accélérer l'ouverture.
17. **Mise en cache** : le contenu des articles est conservé en base (jusqu'à `maxItems` par flux).

### 1.2 Fonctionnalités supprimées (ne pas implémenter)

Plugins ; page `plugins` ; partage Shaarli (option, route, raccourci `s`) ; protection de session par IP (option `disableSessionProtection`) ; mode de visibilité public/protégé ; `?add&newfeed=` ; thèmes multiples ; option `autoUpdate` ; option `autoreadPage` ; personnalisation de l'ordre du menu et de la pagination ; vérification de nouvelle version (`UPDATECHECK_INTERVAL`) ; contrôle de la configuration PHP dans la page d'aide.

### 1.3 Options de configuration

Reprises depuis `FeedConf.php` et `config.html`. Défauts PHP entre parenthèses.

| Option | Sens | Défaut |
|--------|------|--------|
| `title` | Titre du lecteur | `mw-feed` |
| `locale` | Langue (en/fr/de) | `en` |
| `redirector` | Préfixe d'anonymisation des liens, ou `noreferrer` | vide |
| `maxItems` | Éléments conservés par flux | 100 |
| `maxUpdate` | Délai maximal (minutes) entre deux mises à jour d'un flux | 60 |
| `order` | `newerFirst` / `olderFirst` | `newerFirst` |
| `view` | `list` / `expanded` | `list` |
| `filter` | `unread` / `all` | `unread` |
| `listFeeds` | `show` / `hide` | `show` |
| `byPage` | Éléments par page | 10 |
| `autoreadItem` | Marquer lu en passant à l'élément suivant | non |
| `autohide` | Masquer un flux/dossier sans élément non lu | non |
| `autofocus` | Sauter automatiquement à l'élément courant | oui |
| `addFavicon` | Télécharger et mettre en cache les favicons | non |
| `preload` | Précharger les éléments de la page | non |
| `blank` | Ouvrir les liens dans un nouvel onglet | non |
| `swipe` | Balayage tactile sur mobile | oui |
| `theme` (nouveau) | `auto` / `light` / `dark` | `auto` |

Supprimés : `autoUpdate`, `autoreadPage`, personnalisation de l'ordre du menu et de la pagination (ordre fixe, non configurable).

---

## 2. Architecture

Un seul module Go, dont la racine est `src/`. Structure :

```
Dockerfile
.github/workflows/ci.yml
docs/PLAN.md
src/go.mod                      # module github.com/mwolff44/mw-feed
src/cmd/mw-feed/main.go         # point d'entrée, flags/env, sous-commande healthcheck
src/internal/config/            # lecture des flags/env (adresse, répertoire de données, proxy, SSRF)
src/internal/store/             # SQLite : migrations, requêtes (pas de SQL ailleurs)
src/internal/feed/              # téléchargement, parsing (gofeed), mise à jour, règles de rétention
src/internal/scheduler/         # ordonnanceur arrière-plan
src/internal/opml/              # import / export
src/internal/auth/              # argon2id, sessions, CSRF, bannissement
src/internal/web/               # handlers, middlewares, rendu
src/internal/web/templates/     # html/template (embed), dont partiels htmx
src/internal/web/static/        # css, js, htmx.min.js, favicon par défaut (embed)
src/internal/i18n/              # chargement JSON, fonction T()
src/internal/i18n/locales/      # en.json fr.json de.json (embed)
src/internal/sanitize/          # bluemonday, politique unique
```

Contraintes :
- Aucune dépendance CGO. `CGO_ENABLED=0` doit compiler.
- Configuration d'exécution minimale par **flags ou variables d'environnement** (noms en section 0, n° 15). Tout le reste est dans la base (section 1.3). Les durées fixes (session 3600 s, bannissement 1800 s, 4 échecs, 30 jours) sont dans le code, pas dans la base.
- Le SQL reste dans `internal/store`. Les handlers appellent des méthodes typées.
- `context.Context` partout, arrêt propre (`SIGTERM`), journal via `log/slog`.

---

## 3. Modèle de données (SQLite)

Relationnel, KISS. Mode WAL, `foreign_keys=ON`, une seule connexion en écriture. Migrations versionnées, embarquées, appliquées au démarrage. Tous les temps sont des **INTEGER (secondes Unix)**.

```sql
-- Réglages : une seule ligne, colonnes typées (section 1.3)
settings(
  id INTEGER PRIMARY KEY CHECK(id=1),
  title TEXT NOT NULL DEFAULT 'mw-feed', locale TEXT NOT NULL DEFAULT 'en',
  redirector TEXT NOT NULL DEFAULT '',
  max_items INTEGER NOT NULL DEFAULT 100, max_update INTEGER NOT NULL DEFAULT 60,
  "order" TEXT NOT NULL DEFAULT 'newerFirst', view TEXT NOT NULL DEFAULT 'list',
  filter TEXT NOT NULL DEFAULT 'unread', list_feeds INTEGER NOT NULL DEFAULT 1,
  by_page INTEGER NOT NULL DEFAULT 10,
  autoread_item INTEGER NOT NULL DEFAULT 0, autohide INTEGER NOT NULL DEFAULT 0,
  autofocus INTEGER NOT NULL DEFAULT 1, add_favicon INTEGER NOT NULL DEFAULT 0,
  preload INTEGER NOT NULL DEFAULT 0, blank INTEGER NOT NULL DEFAULT 0,
  swipe INTEGER NOT NULL DEFAULT 1, theme TEXT NOT NULL DEFAULT 'auto'
)
user(id INTEGER PRIMARY KEY CHECK(id=1), login TEXT NOT NULL, password_hash TEXT NOT NULL)
sessions(id TEXT PRIMARY KEY, csrf_token TEXT NOT NULL, created_at INTEGER NOT NULL,
         last_seen_at INTEGER NOT NULL, persistent INTEGER NOT NULL)
login_failures(ip TEXT PRIMARY KEY, count INTEGER NOT NULL, last_failure_at INTEGER NOT NULL, banned_until INTEGER)

folders(id INTEGER PRIMARY KEY, title TEXT NOT NULL UNIQUE,
        position INTEGER NOT NULL,                       -- ordre manuel
        collapsed INTEGER NOT NULL DEFAULT 0)
feeds(id INTEGER PRIMARY KEY, xml_url TEXT NOT NULL UNIQUE, html_url TEXT, title TEXT NOT NULL, description TEXT,
      update_interval INTEGER,                           -- NULL = auto (maxUpdate), sinon minutes
      last_update_at INTEGER, last_error TEXT,
      last_warning TEXT,                                 -- clé de traduction (ex. 'items_missed')
      http_etag TEXT, http_last_modified TEXT,           -- validateurs de la dernière réponse 200
      last_item_time INTEGER)
feed_folders(feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
             folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
             PRIMARY KEY(feed_id, folder_id))
items(id INTEGER PRIMARY KEY,
      feed_id INTEGER REFERENCES feeds(id) ON DELETE SET NULL,   -- NULL seulement pour un étoilé orphelin
      feed_title TEXT NOT NULL,                                  -- titre du flux d'origine, toujours renseigné
      link TEXT NOT NULL, title TEXT, via TEXT, author TEXT, description TEXT, content TEXT,
      published_at INTEGER NOT NULL,
      is_read INTEGER NOT NULL DEFAULT 0, starred_at INTEGER,
      UNIQUE(feed_id, link))
favicons(feed_id INTEGER PRIMARY KEY REFERENCES feeds(id) ON DELETE CASCADE, data BLOB NOT NULL, fetched_at INTEGER NOT NULL)
```

Index sur `items(feed_id, is_read, published_at)` et `items(starred_at)`.

Notes :
- La déduplication d'un élément se fait par son **lien** (`UNIQUE(feed_id, link)`). Un élément sans lien est ignoré.
- **Suppression d'un flux** : faite par le code, dans une transaction (pas de trigger) : supprimer les éléments non étoilés du flux, les étoilés passent à `feed_id = NULL` (`ON DELETE SET NULL`). `feed_title` garantit l'affichage.
- **Désétoiler** un élément dont `feed_id` est NULL le supprime (même transaction).
- Un élément étoilé n'est jamais supprimé par la rétention (4.3).
- `last_item_time` est conservé : la rétention peut supprimer l'élément le plus récent, ce qui fausserait la règle de nouveauté de 4.3.
- **Ordre** : les flux sont triés par titre (ordre naturel, insensible à la casse), sans ordre manuel (comportement PHP `sortFeeds`). Les dossiers ont un ordre manuel (`folders.position`, comportement PHP `sortFolders`/`orderFolder`).
- **Étoilés** : page triée par `starred_at` décroissant (étoile la plus récente d'abord), comme en PHP.
- Favicons en BLOB dans la base : un seul fichier `.db` à sauvegarder.

---

## 4. Règles métier

### 4.1 Ajout d'un flux
1. Valider l'URL (http/https uniquement).
2. Télécharger et parser (gofeed). Si échec : message d'erreur, rien n'est créé.
3. Créer le flux (titre, URL du site, description tirés du flux), l'associer aux dossiers choisis (créer le nouveau dossier si fourni), puis importer ses éléments (4.3).
4. Rediriger vers la vue de ce flux.
5. Si l'URL existe déjà : erreur explicite.

### 4.2 Ordonnanceur
- Boucle en arrière-plan, tick régulier (par défaut chaque minute, valeur interne, pas configurable).
- À chaque tick, sélectionner les flux dus : `now - last_update_at > délai * 60 s`.
- Délai effectif : `update_interval` NULL (auto) → `maxUpdate`. Un nombre est accepté s'il est compris entre **5** (`MIN_TIME_UPDATE`) et `maxUpdate`, sinon retombe sur auto (NULL).
- Concurrence bornée (par défaut 4 téléchargements simultanés), timeout HTTP par flux (par défaut 30 s), `User-Agent` générique sans donnée personnelle.
- Une erreur sur un flux n'arrête pas les autres : enregistrer `last_error`.
- Même verrou/protection pour qu'une mise à jour manuelle et une mise à jour planifiée du même flux ne s'exécutent pas en parallèle.
- Mise à jour manuelle : flux, dossier ou tout. Elle ignore le délai (équivalent de `force`) et n'envoie pas d'en-têtes conditionnels (décision n° 34).

### 4.3 Mise à jour d'un flux (reprise de `Feed::updateChannel`)
1. Télécharger et parser. En cas d'erreur : renseigner `last_error`, conserver les données existantes.
2. Ne considérer que les `maxItems` éléments les plus récents du flux.
3. Un élément est **nouveau** s'il n'existe pas déjà (même `link`) **et** si sa date est postérieure à la date du plus récent élément déjà connu du flux. Date absente ou invalide : date courante.
4. Pour un nouvel élément :
   - `via` : conserver l'URL `guid` seulement si c'est une URL dont l'hôte diffère de celui du lien, sinon vide ; si vide, remplacer par l'URL du site du flux.
   - `author` : `Titre du flux (auteur)` si un auteur existe, sinon `Titre du flux`.
   - `description` : texte brut (balises retirées), tronqué à **500 caractères** (en caractères Unicode, pas en octets).
   - `content` : contenu HTML **assaini** (`bluemonday`), plus le rendu des pièces jointes (4.4).
5. **Avertissement** « Items may have been missed since last update » (à traduire) : si le flux avait déjà des éléments et que **tous** les éléments récupérés sont nouveaux.
6. **Rétention** : supprimer les éléments **lus** qui ne figurent plus dans le flux, hors étoilés ; puis si le total dépasse `maxItems`, supprimer les plus anciens, hors étoilés. Les éléments **non lus** absents du flux sont conservés tant que le quota n'est pas dépassé.
7. Recalculer les compteurs (non lus, total) par requête, pas de colonnes dénormalisées sauf nécessité mesurée.

### 4.4 Pièces jointes et miniatures (reprise de `Feed::showEnclosure`)
Ajouter au contenu, après `<br><br>` pour les pièces jointes et `<br>` pour la miniature :
- extension `jpg jpeg png gif` → `<img src>` ;
- `mp3 oga wav` → `<audio controls>` ;
- `mp4 ogg webm` → `<video controls>` ;
- URL sans extension commençant par `https://www.youtube.com` → `<iframe>` vers `www.youtube-nocookie.com` (`/v/` → `/embed/`), `allowfullscreen`, `referrerpolicy="strict-origin-when-cross-origin"` ;
- sinon : lien `<a>`.

La politique `bluemonday` doit autoriser ces balises produites par l'application (`img`, `audio`, `video`, `source`, `iframe` limité à `youtube-nocookie.com`), mais **pas** celles du flux (scripts, iframes arbitraires, handlers `on*`).

### 4.5 Marquage lu / non lu
Applicable à : un élément, un flux, un dossier, tout. Un marquage « tout lu » sur une sélection filtrée par `unread` affecte toute la sélection, pas seulement la page courante.

### 4.6 Sélections
Une sélection courante est : tout, un dossier, ou un flux. Les listes d'éléments, les compteurs, la mise à jour et le marquage lu respectent cette sélection. Les étoilés ont la même notion de sélection.

### 4.7 Anonymisation des liens
Si `redirector` est non vide, préfixer l'URL des liens externes des articles (pas images ni médias). Si `redirector = noreferrer`, ajouter `rel="noreferrer"` à la place. Appliquer côté serveur au rendu du contenu.

### 4.8 Favicons
- Source : `https://icons.duckduckgo.com/ip3/<hôte du site>.ico` (comme en PHP).
- Option `addFavicon` activée : télécharger une fois, stocker en local, servir depuis le serveur.
- Option désactivée : afficher une icône par défaut embarquée dans le binaire. Aucune requête vers un service tiers.

---

## 5. Routes

Les URLs ne sont pas imposées. Règles :
- Pages en GET, **actions qui modifient l'état en POST** avec jeton CSRF (contrairement à PHP qui utilise des GET). Chaque action doit fonctionner **sans JavaScript** (formulaire + redirection) ; le JS ne fait qu'améliorer (requêtes en arrière-plan, mise à jour partielle).
- Tout est protégé par la session, sauf `/login`, `/install` (tant que non installé) et les fichiers statiques.

Pages minimales : installation, connexion, déconnexion, accueil/liste d'éléments (sélection + vue + filtre + page), étoilés, ajout de flux, édition (tout / dossier / flux), import, export, configuration, mot de passe, aide, favicon d'un flux.

---

## 6. Interface

### 6.1 Principes
- HTML sémantique, accessible au clavier, responsive (mobile compris).
- CSS écrit à la main, variables CSS pour le clair/sombre, aucun framework CSS.
- htmx (embarqué, sans étape de build) plus JS maison pour ce que htmx ne couvre pas (raccourcis, swipe, `autofocus`, `preload`). Pas de script inline (CSP). Chaque fonction JS dégrade vers un formulaire fonctionnel.
- Texte uniquement via `T("clé")` (i18n), aucune chaîne en dur dans les templates.

### 6.2 Comportements JS à reproduire (référence : `src/inc/script.js`)
Ouvrir/fermer un élément en vue liste ; marquer lu/étoiler sans recharger ; chargement des éléments suivants au défilement ; mise à jour des compteurs ; `autofocus` ; `autohide` ; `preload` ; `swipe` (mobile : précédent/suivant) ; ouverture dans un nouvel onglet selon `blank`.

### 6.3 Raccourcis clavier (reprise de `help.html`, hors partage)
- **Éléments** : `Espace` ou `t` basculer l'élément courant ; `m` lu/non lu ; `Maj+m` lu/non lu et ouvrir ; `→` ou `n` suivant ; `←` ou `p` précédent ; `Maj+n` / `Maj+p` page suivante/précédente ; `j` / `k` suivant/précédent et ouvrir (vue liste) ; `o` ouvrir dans un nouvel onglet ; `Maj+o` ouvrir dans la fenêtre courante ; `a` tout marquer lu (sélection courante) ; `*` étoiler/désétoiler ; `z` ouvrir tous les non lus de la page dans des onglets et les marquer lus.
- **Menu** : `h` accueil ; `v` changer de vue ; `f` afficher/masquer la liste des flux ; `e` éditer la sélection ; `u` mettre à jour la sélection ; `r` recharger ; `?` ou `F1` aide.

(`s` partager : supprimé avec Shaarli.)

---

## 7. Internationalisation

- `internal/i18n/locales/{en,fr,de}.json`, clé → texte. Anglais = langue de référence ; une clé manquante dans une autre langue retombe sur l'anglais.
- Point de départ : `../kriss_feed/src/po/messages/{messages.pot,fr.po,de.po}`. Réutiliser les traductions existantes quand le texte est identique ; générer les nouvelles chaînes en fr/de puis les signaler comme **à relire** (liste dans le rapport final).
- Test : chaque clé présente en `en` existe en `fr` et `de`, et réciproquement ; chaque clé utilisée dans un template existe en `en`.
- Formats de date selon la langue.

---

## 8. Sécurité

- Mot de passe : argon2id (19 MiB, t=2, p=1, sel aléatoire, format PHC) ; comparaison à temps constant.
- Session : identifiant aléatoire (≥ 128 bits) en base ; cookie `HttpOnly`, `SameSite=Lax`, `Secure` si la requête est en HTTPS (via `X-Forwarded-Proto` **uniquement** si l'option « faire confiance au proxy » est active) ; expiration après **3600 s** d'inactivité ; « rester connecté » = session persistante de **30 jours**, renouvelée à chaque visite. Régénérer l'identifiant à la connexion.
- CSRF : jeton par session, vérifié sur tout POST.
- Bannissement : **4** échecs consécutifs pour une même IP → **1800 s** de blocage (message explicite). Identifier l'IP par `RemoteAddr`, ou par `X-Forwarded-For` (première adresse) si l'option « faire confiance au proxy » est active (flag/variable d'environnement, désactivée par défaut).
- En-têtes : `Content-Security-Policy` stricte (scripts du même domaine, images et médias autorisés depuis n'importe quelle origine, `frame-src` limité à `www.youtube-nocookie.com`), `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `X-Frame-Options: DENY`.
- Contenu des flux : toujours assaini avant stockage ET échappé en sortie pour les champs texte.
- Téléchargement de flux : protéger contre le SSRF (refuser les adresses loopback/privées/lien-local après résolution DNS et à chaque redirection) sauf si l'option dédiée (flag/variable d'environnement) les autorise ; limiter la taille de réponse (par défaut 10 Mo), limiter les redirections (10).
- Import OPML : limiter la taille, refuser les entités XML externes.

---

## 9. Tests, qualité, CI, Docker

### 9.1 Tests unitaires
Hachage et sessions ; bannissement ; parsing et normalisation de flux (RSS, Atom, JSON Feed, flux invalides) ; règles de 4.3 (nouveauté, `via`, `author`, troncature 500, avertissement « missed », rétention, quota, étoilés préservés) ; pièces jointes (4.4) ; assainissement ; OPML import/export (dossiers imbriqués, aller-retour) ; i18n (cohérence des clés) ; pagination, filtre, tri, sélections ; délai de mise à jour (4.2).

### 9.2 Tests d'intégration HTTP
`httptest` sur le serveur complet, base SQLite temporaire, faux serveur de flux (`httptest.Server`). Parcours : installation → connexion → ajout de flux (succès et échec) → mise à jour → lecture/non lu (élément, flux, dossier, tout) → étoiles → dossiers → édition → import/export → changement de mot de passe → bannissement. Vérifier le CSRF (POST sans jeton refusé) et que toute page non publique redirige vers la connexion.

### 9.3 Qualité
`gofmt`, `go vet`, `golangci-lint` (fichier de config versionné), `go test -race ./...`.

### 9.4 CI (GitHub Actions)
Workflow sur push et PR : lint, tests avec `-race`, build `CGO_ENABLED=0`, build de l'image Docker (sans publication).

### 9.5 Docker
Build multi-étapes (compilation depuis `src/`), image finale `gcr.io/distroless/static:nonroot`, utilisateur non root, volume pour le répertoire de données, port exposé documenté, route `/healthz` et `HEALTHCHECK` via `mw-feed healthcheck` (pas de shell dans l'image).

---

## 10. Phases d'implémentation

Chaque phase se termine par `go vet`, `go test -race ./...` verts et un commit. Une phase n'est terminée que si ses critères d'acceptation sont vérifiés.

**Phase 0 : initialisation**
Dépôt, `go mod init` dans `src/`, licence GPL v3 (fichier `LICENSE`), README, structure de la section 2, `golangci-lint`, CI minimale, Dockerfile minimal.
*Acceptation* : `go build ./...` OK sans CGO ; CI verte ; `docker build` OK.

**Phase 1 : stockage et configuration**
Migrations SQLite, couche `store`, ligne unique `settings` avec les défauts de 1.3, lecture des flags/env.
*Acceptation* : tests de migration sur base vide ; lecture/écriture de chaque réglage ; démarrage sur répertoire de données vide.

**Phase 2 : authentification**
Installation, connexion, déconnexion, sessions, CSRF, bannissement, changement de mot de passe, pages et middleware d'accès.
*Acceptation* : tests unitaires de 9.1 pour l'auth ; parcours d'intégration d'accès (non connecté → login) ; bannissement après 4 échecs ; expiration par inactivité (horloge injectable).

**Phase 3 : flux, dossiers, parsing**
Téléchargement sécurisé (section 8), parsing gofeed, ajout de flux, dossiers, flux multi-dossiers, édition, suppression, règles 4.3 et 4.4, assainissement.
*Acceptation* : tests unitaires 4.3/4.4 ; ajout d'un flux via faux serveur ; refus SSRF ; flux invalides renvoient une erreur propre.

**Phase 4 : éléments et lecture**
Listes, sélections, filtre, ordre, pagination, marquage lu/non lu (tous niveaux), étoiles et page des étoilés, compteurs.
*Acceptation* : tests table-driven sur sélections × filtre × ordre ; étoilés conservés après rétention et suppression du flux.

**Phase 5 : ordonnanceur**
Boucle, sélection des flux dus, concurrence bornée, exclusion mutuelle, mise à jour manuelle (flux/dossier/tout), arrêt propre.
*Acceptation* : tests avec horloge simulée ; deux mises à jour concurrentes du même flux n'écrivent pas deux fois ; `SIGTERM` termine proprement.

**Phase 6 : OPML**
Import (dossiers imbriqués, doublons ignorés, erreurs de format) et export.
*Acceptation* : aller-retour export → import identique ; entités externes refusées.

**Phase 7 : interface sans JavaScript**
Templates, CSS clair/sombre, toutes les pages et actions par formulaire, configuration complète, aide, i18n branchée, favicons, anonymisation des liens, en-têtes de sécurité.
*Acceptation* : toutes les fonctions de 1.1 utilisables **sans JS** ; tests d'intégration des parcours de 9.2 ; test i18n de cohérence des clés.

**Phase 8 : amélioration JavaScript**
Comportements de 6.2, raccourcis de 6.3, précharge, `autofocus`, `autohide`, `swipe`, `blank`.
*Acceptation* : chaque comportement testé manuellement avec la liste de contrôle ; l'application reste fonctionnelle si le JS est désactivé.

**Phase 9 : finitions**
Docker final (utilisateur non root, volume, santé), README (installation, configuration, sauvegarde du fichier `.db`, reverse proxy exemple), vérification de toutes les fonctionnalités de 1.1 par rapport à cette liste.
*Acceptation* : checklist de 1.1 cochée dans le rapport final ; liste des chaînes fr/de générées à relire ; CI verte.

---

## 11. Hors périmètre

Tout ce que la section 1.2 supprime, plus : multi-utilisateur, API JSON, TLS intégré, sous-chemin, binaires multiplateformes, migration de données PHP, idées de `todo.org`, plugins.

---

## 12. Décisions ouvertes

1. **Format exact du crédit** (section 13) : l'agent propose le texte, le propriétaire valide.

Toutes les autres décisions sont arrêtées (section 0). Nom : `mw-feed`, module `github.com/mwolff44/mw-feed`.

---

## 13. Crédit et licence

- Le projet est une **réécriture indépendante** du comportement de KrISS feed, application PHP créée par **Tontof** (http://tontof.net, https://github.com/tontof/kriss_feed). Le crédit doit apparaître :
  - dans le `README` (section « Crédits » : projet d'origine, auteur, liens) ;
  - dans la page d'aide de l'interface (mention et lien vers le projet d'origine) ;
  - dans le fichier `LICENSE` ou un fichier `NOTICE` adjacent.
- Licence du nouveau projet : **GNU GPL v3** (choix du propriétaire, en cohérence avec l'esprit « copyleft » de l'original). Ajouter un en-tête de licence court (`SPDX-License-Identifier: GPL-3.0-or-later`) dans chaque fichier source Go. Vérifier que toutes les dépendances (`modernc.org/sqlite`, `gofeed`, `bluemonday`, etc.) ont une licence compatible avec la GPL v3 et lister leurs licences dans `NOTICE`.
- **Ne copier aucun code ni asset du dépôt PHP** (PHP, templates RainTPL, `script.js`, CSS, thèmes, images, `flags.png`, `favicon.ico`). Le PHP est une spécification de comportement uniquement. La licence du projet d'origine est décrite comme « Copyleft (ɔ) » sans licence SPDX précisée : toute reprise de code poserait une question de compatibilité avec la GPL v3. Les traductions fr/de du fichier `.po` sont reprises seulement pour les **chaînes de texte** équivalentes (à signaler dans `NOTICE` ; voir la section 7). Si le moindre doute existe sur la reprise d'un élément, demander au propriétaire.
