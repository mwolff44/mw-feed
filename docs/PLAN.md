# Plan de réécriture de KrISS feed en Go

> Nom du projet : **mw-feed**. Module Go : `github.com/mwolff44/mw-feed`. Binaire : `mw-feed`. Ne pas réutiliser la marque « KrISS feed » dans le nom, l'interface ou la documentation, sauf pour le crédit (section 13).

Document destiné à un agent de développement. Il décrit **quoi** construire, les décisions déjà prises par le propriétaire du projet, et un découpage en phases vérifiables. Ce qui n'est pas décidé est listé en section 12 : **ne pas trancher soi-même, demander**.

Référence fonctionnelle : l'application PHP du dépôt `kriss_feed` (`src/kriss_feed.php`, `src/class/*.php`, `src/class/tpl/*.html`, `src/inc/script.js`). Elle sert de spécification de comportement, pas de modèle d'architecture.

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
| 7 | Interface | **Modernisée** (nouveau design), rendue côté serveur avec `html/template`, CSS écrit à la main, JS léger sans étape de build (htmx autorisé). **Utilisable sans JavaScript.** Pas de SPA. Pas d'API JSON publique. |
| 7c | Thèmes | Clair et sombre. Réglage dans la configuration : automatique (`prefers-color-scheme`), clair, sombre. Les 11 thèmes actuels sont abandonnés. |
| 8 | Fonctionnalités supprimées | Partage Shaarli, protection de session par IP, ajout de flux par URL en paramètre (`?add&newfeed=`). |
| 8 | Fonctionnalités conservées | Anonymisation des liens (redirector), favicons, précharge/cache, réglages `blank`, `swipe`, `autofocus`. |
| 5b | Visibilité | **Privé uniquement** : tout exige une connexion. Pas de mode public ni protégé. |
| 9 | Authentification | Un seul utilisateur, mot de passe haché (bcrypt ou argon2id), formulaire d'installation au premier lancement, session par cookie, CSRF, expiration par inactivité (3600 s), bannissement après 4 échecs pendant 1800 s, case « rester connecté », changement de mot de passe. Valeurs **fixes**. |
| 10 | i18n | Fichiers JSON embarqués, un par langue : **en** (défaut), **fr**, **de**. Langue réglée dans la configuration. |
| 11 | Outils | Go dernière stable (≥ 1.25) ; `net/http` (ServeMux) ; `html/template` ; `modernc.org/sqlite` ; `github.com/mmcdole/gofeed` ; OPML maison avec `encoding/xml` ; `github.com/microcosm-cc/bluemonday`. |
| 11 | Dépôt, licence | **Nouveau dépôt**, licence **GPL v3** (GPL-3.0-or-later, texte intégral dans `LICENSE`). |
| 12a | Crédit | Le projet est une réécriture de **KrISS feed**, créé par **Tontof** (http://tontof.net, https://github.com/tontof/kriss_feed). Le crédit est obligatoire (voir section 13). |
| 12b | Port | Port par défaut **8080**. |
| 12c | Reverse proxy | Option (désactivée par défaut) « faire confiance au proxy » : IP lue dans `X-Forwarded-For`, protocole dans `X-Forwarded-Proto`. Pas de liste de proxys. |
| 12d | « Rester connecté » | **30 jours**, renouvelés à chaque visite. |
| 12e | SSRF | Adresses loopback/privées/lien-local bloquées par défaut ; option (flag ou variable d'environnement) pour les autoriser. |
| 12f | Réglages supprimés | `autoUpdate`, `autoreadPage`, personnalisation de l'ordre du menu et de la pagination (ordre fixe). |
| 12g | Étoilés | Conservés quand leur flux est supprimé, avec le nom du flux d'origine mémorisé. |
| 12h | Favicon désactivé | Icône par défaut embarquée (aucune requête tierce). |
| 12 | Tests | Unitaires, intégration HTTP (`httptest`, SQLite temporaire, faux serveur de flux), `go vet` + `golangci-lint` + `go test -race`, CI GitHub Actions (tests + build de l'image Docker). Pas de tests de parité avec l'application PHP. |

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
| `title` | Titre du lecteur | `Kriss feed` |
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

Un seul module Go. Proposition de structure (à ajuster si justifié) :

```
cmd/mw-feed/main.go      # point d'entrée, flags/env, démarrage
internal/config/            # lecture des flags/env (adresse, répertoire de données)
internal/store/             # SQLite : migrations, requêtes (pas de SQL ailleurs)
internal/feed/              # téléchargement, parsing (gofeed), mise à jour, règles de rétention
internal/scheduler/         # ordonnanceur arrière-plan
internal/opml/              # import / export
internal/auth/              # hachage, sessions, CSRF, bannissement
internal/web/               # handlers, middlewares, rendu
internal/web/templates/     # html/template (embed)
internal/web/static/        # css, js, favicon par défaut (embed)
internal/i18n/              # chargement JSON, fonction T()
internal/i18n/locales/      # en.json fr.json de.json (embed)
internal/sanitize/          # bluemonday, politique unique
Dockerfile
.github/workflows/ci.yml
```

Contraintes :
- Aucune dépendance CGO. `CGO_ENABLED=0` doit compiler.
- Configuration d'exécution minimale par **flags ou variables d'environnement** : adresse d'écoute (défaut `:8080`), répertoire de données (base SQLite + favicons). Tout le reste est dans la base (section 1.3).
- Le SQL reste dans `internal/store`. Les handlers appellent des méthodes typées.
- `context.Context` partout, arrêt propre (`SIGTERM`), journal via `log/slog`.

---

## 3. Modèle de données (SQLite)

Mode WAL, `foreign_keys=ON`, une seule connexion en écriture. Migrations versionnées, embarquées, appliquées au démarrage.

```
settings(key TEXT PRIMARY KEY, value TEXT NOT NULL)          -- section 1.3
user(id INTEGER PRIMARY KEY CHECK(id=1), login TEXT NOT NULL, password_hash TEXT NOT NULL)
sessions(id TEXT PRIMARY KEY, csrf_token TEXT NOT NULL, created_at, last_seen_at, persistent INTEGER NOT NULL)
login_failures(ip TEXT PRIMARY KEY, count INTEGER NOT NULL, last_failure_at, banned_until)
folders(id INTEGER PRIMARY KEY, title TEXT NOT NULL, position INTEGER NOT NULL, collapsed INTEGER NOT NULL DEFAULT 0)
feeds(id INTEGER PRIMARY KEY, xml_url TEXT NOT NULL UNIQUE, html_url TEXT, title TEXT NOT NULL, description TEXT,
      position INTEGER NOT NULL, update_interval TEXT NOT NULL DEFAULT 'auto',  -- 'auto' | 'max' | minutes
      last_update_at, last_error TEXT, last_item_time)
feed_folders(feed_id, folder_id, PRIMARY KEY(feed_id, folder_id))
items(id INTEGER PRIMARY KEY, feed_id (NULL après suppression du flux si étoilé), feed_title TEXT, link_hash TEXT NOT NULL, title TEXT, link TEXT NOT NULL, via TEXT,
      author TEXT, description TEXT, content TEXT, published_at INTEGER NOT NULL,
      is_read INTEGER NOT NULL DEFAULT 0, starred_at INTEGER,
      UNIQUE(feed_id, link_hash))
favicons(feed_id PRIMARY KEY, data BLOB, fetched_at)           -- ou fichiers dans le répertoire de données : au choix de l'agent
```

Index sur `items(feed_id, is_read, published_at)`, `items(starred_at)`.

Notes :
- La clé de déduplication d'un élément est le **lien** (hash du lien), comme en PHP (`smallHash(link)`). Un élément sans lien est ignoré.
- Un élément étoilé n'est jamais supprimé par la rétention (4.3), et reste visible dans la page des étoilés même si son flux est supprimé (dans ce cas, le titre du flux d'origine est mémorisé sur l'élément pour l'affichage ; `feed_id` devient NULL à la suppression du flux).
- L'ordre d'affichage des flux et dossiers est stocké (`position`), avec tri par titre ou par ordre manuel comme en PHP (`sortFeeds`, `sortFolders`).

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
- Délai effectif : `auto` ou `max` → `maxUpdate`. Un nombre est accepté s'il est compris entre **5** (`MIN_TIME_UPDATE`) et `maxUpdate`, sinon retombe sur `auto`.
- Concurrence bornée (par défaut 4 téléchargements simultanés), timeout HTTP par flux (par défaut 30 s), `User-Agent` générique sans donnée personnelle.
- Une erreur sur un flux n'arrête pas les autres : enregistrer `last_error`.
- Même verrou/protection pour qu'une mise à jour manuelle et une mise à jour planifiée du même flux ne s'exécutent pas en parallèle.
- Mise à jour manuelle : flux, dossier ou tout. Elle ignore le délai (équivalent de `force`).

### 4.3 Mise à jour d'un flux (reprise de `Feed::updateChannel`)
1. Télécharger et parser. En cas d'erreur : renseigner `last_error`, conserver les données existantes.
2. Ne considérer que les `maxItems` éléments les plus récents du flux.
3. Un élément est **nouveau** s'il n'existe pas déjà (même `link_hash`) **et** si sa date est postérieure à la date du plus récent élément déjà connu du flux. Date absente ou invalide : date courante.
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
- JS sans dépendance (ou htmx), sans étape de build, embarqué. Chaque fonction JS dégrade vers un formulaire fonctionnel.
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
- Point de départ : `src/po/messages/{messages.pot,fr.po,de.po}`. Réutiliser les traductions existantes quand le texte est identique ; générer les nouvelles chaînes en fr/de puis les signaler comme **à relire** (liste dans le rapport final).
- Test : chaque clé présente en `en` existe en `fr` et `de`, et réciproquement ; chaque clé utilisée dans un template existe en `en`.
- Formats de date selon la langue.

---

## 8. Sécurité

- Mot de passe : bcrypt (coût ≥ 12) ou argon2id ; comparaison à temps constant.
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
Build multi-étapes, image finale minimale (distroless ou scratch avec certificats CA), utilisateur non root, volume pour le répertoire de données, port exposé documenté, `HEALTHCHECK` ou route de santé simple.

---

## 10. Phases d'implémentation

Chaque phase se termine par `go vet`, `go test -race ./...` verts et un commit. Une phase n'est terminée que si ses critères d'acceptation sont vérifiés.

**Phase 0 : initialisation**
Dépôt, `go mod init`, licence GPL v3 (fichier `LICENSE`), README, structure de la section 2, `golangci-lint`, CI minimale, Dockerfile minimal.
*Acceptation* : `go build ./...` OK sans CGO ; CI verte ; `docker build` OK.

**Phase 1 : stockage et configuration**
Migrations SQLite, couche `store`, table `settings` avec les défauts de 1.3, lecture des flags/env.
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
