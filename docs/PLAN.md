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
| 39 | Précisions de la phase 3 (prises à l'implémentation) | Lien, `via`, URL du site et URL de pièces jointes : **http(s) absolues uniquement** (un lien `javascript:` ou relatif n'est jamais conservé) ; les URL relatives sont aussi retirées du HTML assaini. Un flux qui répond **sans aucun élément** ne déclenche ni avertissement ni rétention (réponse peut-être transitoire). `ReorderFolders` exige la liste de **tous** les dossiers, chacun une fois. Le service expose aussi `StarredItems` (étoilés, plus récemment étoilé d'abord) et `DeleteFolder`. `UpdateFeed` enregistre l'erreur dans `last_error` **et** la renvoie. Les doublons de lien dans un même flux sont ignorés. |
| 40 | Périmètre phase 4 | Logique uniquement (pas de page web). Quatre seams : liste d'éléments, compteurs, marquage en masse, page des étoilés. |
| 41 | Pagination | Taille de page : réglage `byPage`, sinon celle de la requête (≥ 1, sinon erreur ; 0 = réglage). Pages numérotées dès 1 ; page < 1 → 1 ; page au-delà de la dernière → **dernière page** (comme le PHP). Résultat : page servie, **nombre de pages (≥ 1, même si vide)**, total. |
| 42 | Ordre | `newerFirst` : date de publication décroissante puis identifiant décroissant ; `olderFirst` : l'inverse exact (date croissante puis identifiant croissant). |
| 43 | Sélections | **Tout** : éléments de tous les flux existants, chacun une fois. **Dossier** : éléments des flux du dossier (un flux sans dossier n'est que dans « Tout » et dans sa propre sélection). **Flux** : ses éléments. Les **orphelins** (étoilés dont le flux est supprimé) sont absents des listes normales. Sélection inconnue : `ErrNotFound`. |
| 44 | Compteurs | Par flux : non lus et total. Par dossier : somme des flux du dossier (un flux dans deux dossiers est compté dans chacun). Global : chaque élément **une seule fois**. Orphelins jamais comptés. Dossiers et flux vides présents avec 0. Calcul par requête, sans colonnes dénormalisées. |
| 45 | Marquage en masse | Porte sur **toute** la sélection (flux, dossier, tout), quels que soient le filtre et la page ; lu **et** non lu ; n'affecte pas les étoiles ; renvoie le nombre d'éléments dont l'état a changé ; sélection inconnue : `ErrNotFound` sans rien modifier ; une seule transaction. |
| 46 | Étoilés | Tri par **date d'étoile décroissante** puis identifiant décroissant (le réglage `order` ne s'applique pas) ; filtre lu/non lu ignoré ; « Tout » inclut les orphelins ; dossier et flux : étoilés de leurs flux ; même pagination (n° 41). |
| 47 | Requêtes de liste | Contenu **complet** dans les listes ; lecture d'un seul élément par identifiant (`Item`, `ErrNotFound`). Filtre ou ordre **absent** = réglage `filter` / `order` ; valeur **invalide** = `ErrInvalidQuery` ; sélection par défaut = « Tout ». Les méthodes minimales de la phase 3 (`Items`, `StarredItems`) quittent le service et ne restent que comme outils de test (`export_test.go`). |
| 48 | Périmètre phase 5 | Logique uniquement (pas de page web). Cinq seams : sélection des flux dus, concurrence bornée et isolation des erreurs, exclusion mutuelle, mise à jour manuelle d'une sélection, cycle de vie (`Run`, arrêt propre, câblage dans `serve`). L'ordonnanceur est dans `internal/scheduler` ; verrou par flux et borne de concurrence sont dans le service de flux, pour que tous les chemins les partagent. |
| 49 | Échéance | Un flux est dû si jamais mis à jour, ou si `maintenant − dernière mise à jour` est **strictement supérieur** au délai effectif. Délai effectif : `maxUpdate` pour `auto` ; un nombre est utilisé tel quel **sauf** s'il est < 5 ou > `maxUpdate` (alors `maxUpdate`), évalué à chaque passage. Un flux en erreur est réessayé au même rythme (la date est renseignée à chaque tentative). |
| 50 | Exclusion mutuelle | Verrou **par flux** dans `UpdateFeed` : un second appel **attend** (en respectant le contexte) puis fait sa mise à jour ; un passage planifié **revérifie** que le flux est encore dû après avoir obtenu le verrou ; une mise à jour manuelle ne revérifie rien. Pas d'erreur « déjà en cours ». |
| 51 | Concurrence | **Une borne globale** de 4 mises à jour simultanées, partagée par tous les chemins (planifié et manuel), non configurable. |
| 52 | Boucle | Intervalle d'**une minute** (interne ; les tests peuvent le réduire via le constructeur). **Premier passage immédiat** au démarrage. Passages **séquentiels** (jamais de chevauchement). Passage sans flux dû : aucun effet. Chaque échec est journalisé au niveau avertissement (identifiant du flux et message, sans donnée personnelle) ; résumé du passage au niveau debug. |
| 53 | Arrêt | À l'annulation du contexte (`SIGTERM`), les téléchargements en cours sont **annulés** et `Run` attend le retour de toutes les mises à jour. Aucune écriture partielle (écriture en une transaction après le téléchargement complet) et **aucune erreur mémorisée** sur un flux dont la mise à jour a été annulée. |
| 54 | Mise à jour manuelle | `UpdateSelection` (flux, dossier, tout) met à jour chaque flux de la sélection **une fois**, dans l'ordre de la liste des flux, en forçant (téléchargement complet, délai ignoré). Renvoie, par flux, son identifiant, son titre, le résultat et l'erreur éventuelle, **après la fin de tous**. Une erreur n'arrête pas les autres ; sélection inconnue : `ErrNotFound` sans téléchargement ; contexte annulé : aucun flux n'est lancé au-delà de la borne, la liste contient **tous** les flux de la sélection (ceux qui n'ont pas fini portent l'erreur de contexte) et l'erreur de contexte est aussi renvoyée. |
| 55 | Périmètre phase 6 | Logique uniquement (pas de page web ni de nom de fichier). Quatre seams : lecture d'un fichier OPML (`internal/opml`, pure), écriture (idem), import dans le lecteur, export et aller-retour. Les écarts avec le PHP qui améliorent ou fiabilisent sont acceptés par le propriétaire s'ils sont signalés. |
| 56 | Dossiers à l'import | Les dossiers imbriqués sont **aplatis** comme en PHP : un flux appartient à **chaque** dossier ancêtre, sans hiérarchie. Un `outline` avec `text`/`title` et sans `xmlUrl` est un dossier, **créé même vide** (écart avec le PHP, nécessaire à l'aller-retour identique) ; un même titre à deux endroits n'est créé qu'une fois ; un titre vide après normalisation rend l'`outline` ignoré. Un même `xmlUrl` répété dans le fichier fusionne ses dossiers. |
| 57 | Sécurité de l'import | Taille maximale **10 Mo** ; toute déclaration `<!ENTITY` (interne ou externe) **refusée** ; profondeur maximale **16** niveaux d'`outline` ; pas de limite du nombre de flux ; racine `opml` (casse et espace de noms ignorés) avec un `body`, sinon « format inconnu » ; encodages **déclarés** pris en charge via `golang.org/x/net/html/charset` (aucun nouveau module), encodage inconnu = erreur explicite. Fichier invalide : erreur, **rien n'est importé**. |
| 58 | Champs à l'import | Titres, descriptions et titres de dossier : **texte brut** (balises retirées, entités décodées, espaces normalisés). Titre du flux : `title`, sinon `text`, sinon nom d'hôte. `xmlUrl` : espaces retirés, **http(s) absolu obligatoire**, sinon le flux est **rejeté** (motif « URL invalide ») sans arrêter l'import. `htmlUrl` invalide : ignoré (vide). |
| 59 | Flux existants | Sans « écraser » : flux existant **intact**, compté « ignoré ». Avec « écraser » : titre, description, URL du site et **liste de dossiers** du fichier remplacent ceux du flux (une description ou un site absents du fichier **effacent** la valeur) ; éléments, lus, étoiles, délai, date de mise à jour et validateurs HTTP sont **conservés**. Nouveaux flux : créés **sans téléchargement**, délai automatique, jamais mis à jour (l'ordonnanceur les télécharge). |
| 60 | Compte rendu | `ImportResult` : flux importés, mis à jour, ignorés, rejetés, dossiers créés ; liste des rejets (titre, URL, motif) limitée aux 100 premiers, compteur exact. Import entier en **une transaction** (erreur de base = rien d'importé ; flux rejetés n'empêchent pas les autres). Fichier sans flux ni dossier : résultat à zéro, pas d'erreur. Dossiers créés dans l'ordre de première apparition, à la suite des existants. |
| 61 | Export | `<?xml` UTF-8, `<opml version="1.0">`, `<head><title>mw-feed</title></head>` ; d'abord les flux **sans dossier**, puis un `outline` par dossier (`text` et `title`), **tous** les dossiers y compris vides, dans l'ordre d'affichage ; flux triés par titre naturel ; un flux de plusieurs dossiers apparaît dans chacun ; attributs d'un flux : `title`, `text`, `xmlUrl`, `type="rss"`, plus `description` et `htmlUrl` **seulement non vides** ; échappement par un vrai encodeur XML (une fois) ; indentation de deux espaces. |
| 62 | Découpage phase 7 | Sept sous-étapes, un seul commit : 7a socle (mise en page, CSS, en-têtes, statiques), 7b i18n complète, 7c lecture, 7d gestion des flux, 7e OPML/configuration/aide, 7f réécriture des liens, 7g favicons. Cinq seams : S1 handler complet (`httptest`, SQLite temporaire, faux serveur de flux), S2 en-têtes de sécurité sur chaque type de réponse, S3 cohérence i18n (clés en/fr/de et clés des templates), S4 réécriture des liens (fonction publique), S5 service de favicons (téléchargeur injectable). Pas de test de template isolé ni de rendu visuel. |
| 63 | URLs de sélection | GET : `/`, `/folder/{id}`, `/feed/{id}`, `/starred`, `/starred/folder/{id}`, `/starred/feed/{id}`, pagination `?page=N`. Actions en POST avec CSRF, retour par un champ caché `return` : n'est accepté qu'un **chemin local** commençant par un seul `/` (pas `//`, pas de schéma), sinon retour à `/` (anti-redirection ouverte). |
| 64 | Affichage | Vue, filtre, ordre et liste des flux se changent par **`POST /display`** (CSRF), champs `view`, `filter`, `order`, `listFeeds` (un ou plusieurs), validés par le store, enregistrés dans `settings` (globaux, un seul utilisateur), puis redirection vers `return`. Chaque entrée de menu est un petit formulaire à bouton (sans JS). |
| 65 | Socle visuel | Un seul CSS écrit à la main, `/static/style.css`, statiques embarqués (`go:embed`) et publics. Le serveur pose `data-theme="light\|dark\|auto"` sur `<html>` (réglage `theme`), `auto` suit `prefers-color-scheme`, sans JS. `<html lang>` selon `locale` (thème et langue globaux, aussi sur installation et connexion). Base commune avec en-tête et menu (sans menu sur installation/connexion). Menu en **texte seul**, sans icônes. Pas de police externe ni de framework CSS. |
| 66 | Thème visuel | Thème **B4** validé (maquette : `docs/design/theme-B4-mockup.html`) : fond papier chaud, lecture en serif (Georgia), densité aérée (colonne de lecture 40 em, rayon 3 px). Clair : accent **bordeaux** `#8a2c47`. Sombre : fond brun chaud, accent **bleu encre** `#8fb0e8`. Couleurs détaillées dans la maquette (variables `--bg --fg --mut --bd --bar --btn --link --ac --sel --code --err --openbg`). |
| 67 | En-têtes de sécurité | Sur **toutes** les réponses : `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src *; media-src *; frame-src https://www.youtube-nocookie.com; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'` ; `X-Content-Type-Options: nosniff` ; `X-Frame-Options: DENY` ; `Referrer-Policy: no-referrer`. `Cache-Control: no-store` sur les pages protégées et d'authentification (pas sur les statiques). `Strict-Transport-Security: max-age=31536000` **uniquement** si la requête est en HTTPS (direct, ou `X-Forwarded-Proto` avec « faire confiance au proxy »). Pas de `Permissions-Policy`. |
| 68 | Menu | Toujours (hors installation/connexion) : titre (accueil), Configuration, Aide, Déconnexion (formulaire POST). Pages de lecture : Vue, Liste des flux, Filtre, Ordre, Mettre à jour, Tout marquer lu, Tout marquer non lu, Éditer (tous sur la **sélection courante**, libellés flux/dossier/tout), lien Étoilés/Éléments. Mot de passe : lien depuis la page Configuration. Ajout de flux, import, export : depuis l'édition globale. Mobile : le menu passe à la ligne (pas de hamburger), boutons assez grands pour le tactile ; `aria-current="page"` sur l'entrée courante. |
| 69 | Messages et erreurs | Message **flash** en session : colonne `flash` (JSON : clé i18n + arguments) sur `sessions` (migration 004), lu et effacé à l'affichage suivant, rendu par `T` et échappé ; niveaux succès (`role="status"`) et erreur (`role="alert"`). Jamais de texte transmis par l'URL. Pages d'erreur dans la même mise en page : 404, 403 (CSRF invalide), 500 (message générique, aucun détail). |
| 70 | Traductions | `fr.json` et `de.json` écrits **au fil de l'eau** : toute nouvelle clé est ajoutée en en/fr/de en même temps (le test de cohérence échoue sinon). Textes identiques aux PO du PHP réutilisés ; les autres générés et listés dans `docs/i18n-review.md` (clé, anglais, français, allemand), complété à chaque ajout, à relire par un locuteur en fin de projet. Tests : mêmes clés dans les trois langues, textes non vides, mêmes verbes de format que l'anglais, toute clé des templates et des littéraux `error.*`/`flash.*` du paquet web existe en anglais. |
| 71 | Dates | `i18n.FormatTime(locale, t)` : `2006-01-02 15:04` (en), `02/01/2006 15:04` (fr), `02.01.2006 15:04` (de), sans noms de mois ni dates relatives, dans le **fuseau du serveur** (`TZ`, sinon UTC) ; `time/tzdata` embarqué pour que `TZ` fonctionne dans l'image distroless. Stockage en UTC inchangé. |
| 72 | Ouverture d'un élément | Vue liste : un `<details>`/`<summary>` natif par élément (résumé : date, flux, titre en gras si non lu, extrait `description`) ; contenu ouvert : titre en lien externe, date complète, auteur, source (`via`), contenu assaini, boutons lu/non lu et étoile. Vue étendue : tout affiché, sans `<details>`. Après une action, redirection vers la même page avec `?open={id}` et l'ancre `#item-{id}` : le serveur rend cet élément ouvert. Pas d'« élément courant » côté serveur (`current`/`next`/`previous` du PHP) : la navigation au clavier est faite par le JS de la phase 8. `web.NewHandler` reçoit aussi le service `feed.Service`. |
| 73 | Actions de lecture | POST + CSRF + `return` : `/item/{id}/read`, `/unread`, `/star`, `/unstar` ; `/mark` (`kind` = `all`\|`folder`\|`feed`, `id`, `read` = `1`\|`0`) marque **toute** la sélection ; `/update` (`kind`, `id`) met à jour la sélection **de façon synchrone** (attend tous les téléchargements, annulée si le navigateur se déconnecte, pas de délai d'écriture HTTP). Élément ou sélection inexistants : 404. Flash de mise à jour : « N flux mis à jour, M nouveaux éléments. » et, s'il y a des échecs, « K flux en erreur » (niveau erreur) ; le détail reste dans la liste des flux (classe d'erreur, message en `title`). Flash de marquage de masse (« N éléments marqués comme lus ») ; pas de flash pour une action sur un seul élément. |
| 74 | Liste des flux | Si `listFeeds = show` : « Tous les éléments » (compteur de non lus), flux sans dossier (titre naturel), puis dossiers dans leur ordre avec leurs flux (un flux de plusieurs dossiers apparaît dans chacun). Ligne = lien vers la sélection + compteur de non lus, `aria-current="page"` sur la sélection courante ; `title` = erreur, sinon description, sinon titre. Dossier = `<details>` ouvert selon `collapsed` (lecture seule en 7c, bascule en 7d). Pages des étoilés : même structure, liens `/starred/...`, **sans compteurs** (pas de nouvelle requête). `hide` : rien. |
| 75 | `autohide` côté serveur | Écart avec le plan (qui le met en JS, phase 8) : si `autohide`, le serveur ne rend pas les flux et dossiers à 0 non lu, sauf la sélection courante, le dossier contenant le flux courant et le dossier courant ; « Tous les éléments » toujours affiché ; sur les étoilés, rien n'est masqué. |
| 76 | Pagination et vide | Titre de page = nom de la sélection + nombre (« N non lus » avec le filtre `unread`, « N éléments » avec `all`) ; `<title>` = « sélection, titre du lecteur ». Pagination en bas si plus d'une page : Page précédente / `N / M` / Page suivante en liens `?page=N`, texte inactif aux bornes, page invalide ramenée à la première ou dernière (pas de redirection). `byPage` réglé seulement dans la configuration. Liste vide : « Aucun élément non lu. » / « Aucun élément. » / « Aucun élément étoilé. ». Identifiant invalide ou inexistant : 404. |
| 77 | Routes de gestion | `GET/POST /add` (ajout) ; `GET /edit` (édition globale) ; `GET/POST /edit/feed/{id}` ; `GET/POST /edit/folder/{id}` ; `POST /folder/{id}/toggle` (repli, retour à `return`). Le lien « Éditer » du menu de lecture mène à `/edit`, `/edit/folder/{id}` ou `/edit/feed/{id}` selon la sélection ; « Ajouter un flux » aussi dans le menu ; l'édition globale mène à l'import et à l'export. Pas de bookmarklet (section 1.2). |
| 78 | Suppression sans JS | Écart avec le PHP (`confirm()` JS) : un flux se supprime par une **page de confirmation** (`GET /edit/feed/{id}/delete`, bouton POST + CSRF, lien Annuler ; flash « Flux supprimé. », retour à l'accueil). Plusieurs flux cochés : page de confirmation qui les liste (identifiants en champs cachés). Un **dossier** se supprime sans confirmation (titre vide ; ses flux sont conservés). |
| 79 | Édition globale | Ordre des dossiers par boutons **Monter / Descendre** (`POST /edit/folder/{id}/up`, `/down`, via `ReorderFolders`), écart avec les champs numériques du PHP. Service : `EditFeedsFolders(feedIDs, addFolderIDs, newFolder, removeFolderIDs)` (ajoute puis retire, dossier nouveau créé une seule fois, tout ou rien, flux ou dossier inconnu = `ErrNotFound`, même dossier à ajouter et retirer = `ErrInvalidRequest`, retourne le nombre de flux) ; `DeleteFeeds(ids)` en une transaction (ignore les inconnus, retourne le nombre supprimé). Aucun flux coché : flash d'erreur « Aucun flux sélectionné. » et retour à `/edit`. Flashs comptés (« Dossiers modifiés pour N flux. », « N flux supprimés. »). Seam S6 : tests du service (tout ou rien, dossier créé une fois, conflit, inconnus, étoilés conservés). |
| 80 | Ajout de flux | Succès : redirection vers `/feed/{id}` + flash « Flux ajouté : *Titre*, N éléments importés. ». Échec : formulaire réaffiché (400) avec message et **valeurs saisies conservées** ; causes : URL invalide, flux déjà présent, téléchargement ou lecture impossible (avec le texte d'erreur du téléchargeur, montré car l'utilisateur est unique et le même texte est déjà dans la liste des flux). Synchrone, annulé si le navigateur se déconnecte. Dossiers : cases + « nouveau dossier ». Pas de `?url=`. |
| 81 | Édition d'un flux | Champs : titre (obligatoire), URL XML (modifiable, sans téléchargement), URL du site, description, dossiers + nouveau dossier, **délai en minutes** (champ numérique ; vide = automatique ; hors [5, `maxUpdate`] = automatique ; pas de « auto »/« max » du PHP), lecture seule : dernière mise à jour (format de la langue), dernière erreur, avertissement (clé traduite). Succès : `/feed/{id}` + « Flux enregistré. ». Échec (titre vide, URL invalide, doublon, délai non numérique) : formulaire réaffiché (400). Boutons Enregistrer, Annuler, Supprimer (confirmation, décision 78). |
| 82 | Édition d'un dossier | Champ titre. Vide : dossier supprimé (flux conservés), redirection `/` + « Dossier supprimé. » (aide : « Laisser vide pour supprimer »). Titre déjà pris : erreur, formulaire réaffiché (400). Succès : `/folder/{id}` + « Dossier enregistré. ». |
| 83 | Import et export | `GET /export` : téléchargement `mw-feed.opml` (`Content-Disposition: attachment`, `text/x-opml; charset=utf-8`). `GET /import` : formulaire (fichier, case « écraser », taille maximale 10 Mo) ; `POST /import` (multipart, CSRF dans le formulaire, corps borné à 10 Mo plus une marge) répond **directement par la page de compte rendu** (200, sans redirection, l'import étant idempotent) : flux importés, mis à jour, ignorés, rejetés, dossiers créés ; liste des rejets (100 premiers, compteur exact) ; bouton « Mettre à jour tous les flux ». Erreurs (formulaire réaffiché en 400, rien importé) : aucun fichier, trop gros, pas un OPML, entité XML refusée, encodage inconnu, profondeur excessive ; messages traduits sans détail technique. Liens Importer/Exporter depuis l'édition globale. |
| 84 | Configuration | `GET/POST /config`. Sections : Général (titre non vide, langue, thème, préfixe d'anonymisation), Lecture (vue, filtre, ordre, liste des flux, éléments par page, éléments conservés, délai maximal ; cases `autohide`, `addFavicon`, `blank`), Comportements JavaScript (cases `autoreadItem`, `autofocus`, `preload`, `swipe`, mention « nécessite JavaScript »). Listes en `<select>`, booléens en cases (décochée = faux), trois nombres entiers ≥ 1, préfixe vide ou `noreferrer` ou URL http(s) absolue. Erreur : formulaire réaffiché (400) avec message et valeurs, rien enregistré. Succès : redirection `/config` + « Réglages enregistrés. ». Lien « Changer le mot de passe » dans cette page ; « Configuration » et « Aide » dans le menu de toutes les pages connectées. Rubriques PHP retirées non reprises (visibilité, Shaarli, protection de session, menus et pagination personnalisables, `autoUpdate`, `autoreadPage`, cron). |
| 85 | Aide et crédits | `GET /help` : présentation (formulaires sans JS, raccourcis avec JS), raccourcis clavier (section 6.3, sans partage, avec la note « nécessitent JavaScript », décrits dès 7e et implémentés en phase 8), À propos (version via `Options.Version`, licence, crédits, renvoi au fichier NOTICE). **Texte des crédits validé** (clôt la décision ouverte n° 12) : « mw-feed est un lecteur de flux écrit en Go. C'est une réécriture indépendante, repartie de zéro, du comportement de KrISS feed, créé par Tontof (https://github.com/tontof/kriss_feed). Aucun code du projet d'origine n'est repris. mw-feed est un logiciel libre, publié sous licence GNU GPL version 3 ou ultérieure. » ; lien externe `rel="noreferrer noopener"` ; pas de lien vers un dépôt de mw-feed (aucun dépôt distant). |
| 86 | Anonymisation des liens | Paquet `internal/anonymize` (fonction pure, seam S4), appliqué **au rendu**, jamais au stockage. Concerne les `<a href>` absolus http(s) du contenu des articles, le lien du titre (`link`) et le lien source (`via`) ; jamais images, médias, iframes, liens relatifs, `#ancre`, `mailto:` ou liens internes. `redirector` vide : rien ; préfixe (`https://anonym.example/?`) : `préfixe + URL` par concaténation sans encodage (comme le PHP) et `rel="noopener noreferrer"` ; `noreferrer` : URL inchangée, `rel="noopener noreferrer"`. `blank` : `target="_blank"` et `rel="noopener"` fusionné, sur tous ces liens, contenu compris (écart avec le PHP où `blank` ne touchait que le titre), sans JS. Sortie reparsée et sérialisée par `golang.org/x/net/html` ; HTML illisible : contenu laissé tel quel. |
| 87 | Favicons | `GET /favicon/{feedID}` (session requise ; id non numérique : 404 ; flux inconnu : icône par défaut). Réglage `addFavicon` désactivé (défaut) : icône par défaut embarquée (`static/default-icon.png`, créée pour le projet), aucune requête tierce. Activé : au premier affichage, téléchargement synchrone (10 s) de `https://icons.duckduckgo.com/ip3/<hôte>.ico`, hôte pris dans l'URL du site, sinon de l'URL XML ; seul `icons.duckduckgo.com` est contacté, jamais l'hôte du flux (et sans l'option `allow-private`). Hôte validé (`[a-z0-9.-]`, ASCII, ≤ 253). Contenu détecté (pas l'en-tête serveur) : PNG, GIF, JPEG, ICO, ≤ 64 Kio ; SVG refusé. Stockage SQLite, migration 005, table `favicons` **par hôte** (les flux d'un même hôte partagent l'icône ; écart avec la table par `feed_id` du §4.6) ; réussite redemandée après 30 jours, échec mémorisé 24 h (icône vide). Réponse : `Cache-Control: private, max-age=86400`, `nosniff`. Affichée (16×16) dans la liste des flux et sur chaque élément ; élément dont le flux est supprimé : icône par défaut. Aide du réglage : DuckDuckGo apprend les sites suivis. |
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
feeds(id INTEGER PRIMARY KEY, xml_url TEXT NOT NULL UNIQUE, html_url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
      update_interval INTEGER,                           -- NULL = auto (maxUpdate), sinon minutes
      last_update_at INTEGER, last_error TEXT NOT NULL DEFAULT '',
      last_warning TEXT NOT NULL DEFAULT '',             -- clé de traduction (ex. 'items_missed')
      http_etag TEXT NOT NULL DEFAULT '', http_last_modified TEXT NOT NULL DEFAULT '',  -- validateurs de la dernière réponse 200
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
favicons(host TEXT PRIMARY KEY, data BLOB NOT NULL, content_type TEXT NOT NULL, fetched_at INTEGER NOT NULL)  -- par hôte, voir décision 87
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
