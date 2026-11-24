# Règles communes, appliquées à inlinr-cli

Repris de 7IBO/socle au commit f0e3f9d, le 24/11.

Ce dépôt vit seul : ce fichier et le [CLAUDE.md](../CLAUDE.md) suffisent pour
y travailler, il n'y a rien à lire ailleurs, et le CLAUDE.md gagne en cas
d'écart. Ce sont les règles que Thibault tient pour toutes ses apps, adaptées
ici ; une règle commune qui change arrive par une PR à elle. Ce qui ne peut
pas valoir pour un binaire en ligne de commande n'est pas repris : interface,
design, accessibilité, perfs web, SEO, serveur partagé. Chat en français ;
commits, PR, code, commentaires, README et notes de release en anglais.

## Règles qui ne se discutent pas

1. **Aucune attribution à Claude ni à Anthropic** : ni `Co-Authored-By`, ni
   `Claude-Session`, ni « Generated with », ni lien de session, ni bloc
   « Requested by », dans les commits, les PR, les commentaires GitHub, les
   noms de branches et les fichiers. Jamais de branche `claude/…`. Le corps de
   chaque PR se relit juste après sa création : l'outil y ajoute un pied,
   qu'on retire avant de la rendre.
2. **Aucun secret dans le dépôt ni en mémoire** : on écrit le nom d'une clé et
   l'endroit où elle vit, jamais sa valeur.
3. **Rien d'irréversible sans le mot de Thibault** : suppression en masse,
   message envoyé hors du projet, et ce qui touche à la production. Ici, la
   production est ce que les machines installent : sans son mot, un tag ou une
   release publiés ne se suppriment ni ne se réécrivent, et la clé de
   signature ne change pas.
4. **Chaque choix se remet en question** avant d'être codé (plus bas), puis
   Claude tranche seul et dit ce qu'il a décidé.
5. **Pas d'avocat ni de comptable** : une question juridique ou fiscale se
   vérifie avec sa source primaire, marquée [A] lue, [B] résumé, [C] connue
   non relue, et un risque résiduel en une phrase.

## Git

- Identité de commit : `Thibault <71017357+7IBO@users.noreply.github.com>`. Un
  conteneur cloud signe « Claude » par défaut : le hook `SessionStart` de
  `.claude/settings.json` règle l'identité et copie `.githooks/` dans
  `.git/hooks/`, dans le cloud seulement. Sur un PC, une fois par clone : les
  deux `git config`, puis `cp .githooks/* .git/hooks/`.
- `.githooks/commit-msg` refuse une ligne d'attribution dans un message ;
  l'étape « No attribution » de la CI relit les commits de la PR et refuse une
  branche `claude/`, mais ne voit pas le corps de la PR. `.githooks/pre-push`
  refuse un push direct sur `main` (garde local, pas une protection de GitHub).
- Une branche par ticket : le `gitBranchName` de Linear, sans le changer (on
  renomme le ticket avant), partie du dernier `origin/main`. Jamais de push
  forcé sur `main`, jamais de réécriture d'une branche partagée.
- Commits en Conventional Commits, en anglais : `type(scope): summary`
  (impératif, minuscules, sans point, 72 caractères au plus), le pourquoi en
  dessous, puis `Refs: THI-NN`. Types : feat, fix, perf, refactor, docs, test,
  build, ci, chore, revert, style ; `!` pour un changement cassant.
- L'id d'un ticket vit dans Linear, la branche, la PR et le message de commit ;
  jamais dans le code, un commentaire, un nom, un test, la doc ou un chemin :
  le pourquoi s'écrit en mots. Aucun contrôle ne le tient ici, il se vérifie
  en relecture.

## Pull requests

1. Ticket Linear prêt : problème, pour qui, proposition, « fait quand »
   vérifiable, mesure après sortie, ce qu'il faudra publier.
2. Branche, PR **en brouillon** dès le premier push : le travail et les
   relectures s'y font. Elle passe prête **une seule fois**, à la fin.
3. Les contrôles de `ci.yml` passent dans la session avant chaque push :
   `gofmt -l .` (aucune sortie), `go vet ./...`, `go build ./...`,
   `go test ./...`, `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`.
4. Deux relectures indépendantes (plus bas), chaque remarque corrigée ou
   répondue dans le corps. Une seule suffit pour une PR de moins de 200 lignes
   changées qui ne touche ni à la sécurité ni à ce qui quitte la machine, et
   pour une PR de docs seules.
5. Merge en squash quand la CI est verte, sans conflit ni remarque ouverte,
   avec un titre et un message explicites (le titre de la PR, son Summary,
   `Refs:`), jamais le message par défaut de GitHub ni de trailer. Une PR dont
   la base a bougé depuis sa dernière CI est mise à jour avant. Ticket en
   Done ; ce qui reste à publier est écrit.
6. Un fil qui s'arrête laisse une note de passation en commentaire du ticket
   (fait, reste, prochaine action, qui la porte).

Titre : `type(scope): summary (THI-NN)`, en anglais, 100 caractères au plus ;
il devient le commit sur `main`. Corps :
[.github/pull_request_template.md](../.github/pull_request_template.md).
Verification dit ce qui a tourné, ce que ça a donné et ce que les relectures
ont changé. Deployment dit « none », ou la release : le numéro de `VERSION` et
l'ordre avec les autres dépôts. Le corps se corrige avec l'outil MCP
`update_pull_request` : un PATCH par `gh api` remet le pied d'attribution.

## Relecture

Les relecteurs sont les agents de `.claude/agents/` : ils n'ont pas écrit le
changement et reçoivent le numéro de PR, le ticket et le diff, **jamais le
récit de l'auteur**. Quand un seul suffit, c'est le premier de la ligne.

| La PR touche | Relecteurs |
|---|---|
| Commandes, file d'attente, client HTTP, MCP, lecture de git ou des manifestes | code, deux fois : justesse et cas limites, puis charge (appels à inlinr.com, verrous SQLite, durée de `heartbeat`) |
| Workflows, signature et mise à jour automatique, jeton, ce qui quitte la machine | code + ops |
| Docs ou outillage seuls | code, deux fois : les faits et commandes, puis l'effet sur les autres règles |

- **Code** : justesse (vide, null, cas limites, plusieurs `inlinr` lancés en
  même temps sur la même file, un verrou tenu pendant un appel réseau) ; rien
  de lu ni d'envoyé sans borne ; sécurité (entrée vers un shell ou un chemin
  d'URL, jeton ou chemin personnel dans les logs ou un signalement) ; coût
  (appels à inlinr.com, durée de `heartbeat`) ; le contrat avec le serveur.
- **Ops** : l'ordre de sortie entre les dépôts, la signature, le retour
  arrière, les secrets et les actions épinglées de `release.yml`, ce qu'une
  release fait sur chaque machine.

Les remarques vont dans la section Reviews du corps (rôle, commit lu, la plus
grave d'abord, chacune avec sa réponse : corrigée, avec le commit, ou pourquoi
pas), jamais en commentaire GitHub : l'outil y ajoute une ligne d'attribution
qu'on ne peut pas retirer. Après les corrections, un relecteur vérifie les
corrections seules.

**Refusent une PR** : un secret, un id de ticket dans le code, une attribution
à une IA ; un drapeau ou un champ du contrat changé sans le changement
correspondant dans les autres dépôts ni ordre de sortie écrit ; une donnée
nouvelle qui quitte la machine sans que le README le dise, ou un service
nouveau qui reçoit des données personnelles sans être nommé dans la politique
de confidentialité d'inlinr.com ; un contrôle contourné (règle désactivée,
test sauté).

## Remettre en question chaque choix

Avant de coder une commande, une dépendance, un appel à un service ou un
changement d'architecture, écrire en quelques lignes : qui s'en sert et
comment on le sait (une fonction que personne n'utilise se retire plutôt que
de s'améliorer) ; ce que ça coûte (appels à inlinr.com, taille du binaire,
maintenance sur trois systèmes) ; la version la plus simple qui rend le même
service, et pourquoi elle ne suffit pas ; les cas (vide, énorme, concurrent,
hors ligne, quelqu'un qui n'est pas l'utilisateur) ; ce qui est vérifié et ce
qui est supposé.

Un petit correctif de bug reste direct. Un ticket se relit avant d'être codé :
s'il est faux, déjà fait ou inutile, on le dit et on le ferme. Une remarque de
relecteur se vérifie, ce n'est pas un ordre.

## Qualité du code

- `gofmt` formate et `go vet` vérifie, tous deux en CI : on ne se bat pas
  contre eux. Une règle qui revient en relecture devient un contrôle, pas une
  phrase de plus dans le CLAUDE.md.
- Un fichier : 600 lignes au plus (un test 1 000), sinon on le découpe. Un
  bloc de 10 lignes ou plus présent à deux endroits devient une fonction
  partagée. Aucun contrôle ne tient ces seuils : ils se vérifient en relecture.
- Un paquet par sujet dans `internal/`, un fichier par commande dans
  `cmd/inlinr/`.
- Dépendances à leur dernière version stable, vérifiées une fois par mois et
  tout de suite pour une faille (`govulncheck` en CI). Pas de bot de mise à jour.
- Un commentaire dit **pourquoi**, en anglais, jamais ce que le code dit déjà :
  12 lignes au plus par bloc, ni id de ticket ni numéro de PR. Un commentaire
  faux se corrige dans la PR qui change le code.
- Un refactor est sa propre PR (`refactor(scope): …`), sans changement de
  comportement, avec les tests qui le prouvent.
- Une règle ou un contrat qui change met à jour, dans la même PR, le CLAUDE.md,
  le README (commandes, clés de configuration) et la section du contrat des
  autres dépôts.

## Textes

La même règle pour le README, les messages de la CLI, les notes de release,
les commits, les PR et les tickets. Des phrases simples, comme un développeur
l'expliquerait à un autre. Pas de tiret long ni de tiret de pause : une
virgule, deux-points, un point ou des parenthèses (un tiret court seulement
entre deux nombres). Ni adverbe de remplissage (`really`, `simply`), ni mot
creux (`seamless`, `leverage`), ni emoji, ni point d'exclamation, ni formule
de machine. Titres en casse de phrase. Une promesse se dit une fois et se
vérifie dans le code : ce que le README dit envoyer ou ne jamais envoyer est
ce que le binaire fait. Aucun contrôle ne tient ces règles ici : elles se
vérifient en relecture.

## CI et releases

- Le dépôt est public : ses minutes GitHub Actions ne sont pas comptées dans
  le budget de l'organisation (3 000 par mois, pour inlinr-web).
- La CI (`ci.yml`) tourne sur les PR seulement, en un seul job, jamais sur un
  push vers `main` : `main` n'est vérifié que par la PR. Elle tourne à chaque
  push sur une PR, brouillon compris (ni filtre sur les brouillons ni
  `timeout-minutes`). Aucune tâche planifiée dans GitHub Actions.
- Une release part quand un merge sur `main` change `VERSION` (X.Y.Z) :
  `release.yml` pose le tag, compile pour cinq cibles, signe `SHA256SUMS.txt`
  et publie. Une PR qui ne veut pas publier ne touche pas à `VERSION`.
- Une release ne se rappelle pas : l'extension VS Code lance `inlinr upgrade`
  60 s après son démarrage puis toutes les 4 h. Le retour arrière est une
  version de plus (un revert mergé, avec un numéro au-dessus). Jamais une
  release pour « voir » un changement (`make build` donne le binaire) ; deux
  par jour au plus, la seconde pour un correctif.
- L'ordre entre les dépôts : inlinr-web d'abord pour un champ ou un point
  d'entrée nouveau, puis la CLI, puis l'extension qui s'en sert (`heartbeat`
  sort en 2 sur un drapeau inconnu).
- Après une release : ses notes disent ce qui change, et
  `inlinr upgrade --check` voit la version.

## Appels à inlinr.com et erreurs

La CLI n'appelle que inlinr.com (ingest, flux d'appareil, `/api/v1/*`) et
GitHub (la dernière release, pour `upgrade`).

- **Un délai à chaque appel**, par le client HTTP ou un `context.WithTimeout` :
  30 s pour tout `heartbeat`, 2 s pour un signalement d'erreur. Écart au
  24/11 : le flux d'appareil (`internal/device/flow.go`) appelle sans délai
  propre.
- **On ne relance que ce qui peut passer**, jamais dans la même exécution : un
  lot que le serveur n'a pas pris (réseau, 5xx) reste dans la file SQLite et
  repart au lancement suivant. Un 400 est jeté, un 401 arrête l'envoi et
  demande `inlinr activate`. Écart au 24/11 : un autre 4xx laisse aussi le lot
  dans la file, et `Retry-After` n'est pas lu.
- **Le serveur n'est pas chargé plus que prévu** : hors enregistrements, un
  beat par fichier toutes les 2 min (`heartbeat_rate_limit_seconds`) ; des
  lots de 25 ; `sync-deps` une fois par jour et par dépôt.
- **Sans réseau ou sans jeton, rien ne casse** : `heartbeat` garde le beat
  dans la file ou s'arrête sans signalement, `mcp` démarre quand même.
- **Ce qui est stocké survit au code qui l'a écrit** : les fichiers de
  `~/.inlinr/` sont écrits par une version et lus par la suivante, et
  `config.toml` est partagé avec le plugin Claude Code. Un format reste
  lisible par l'ancienne version ; une clé de configuration nouvelle s'écrit
  aussi dans le README.
- **Une erreur arrive à un endroit que quelqu'un lit** : `internal/report`
  l'envoie à Admin → Errors d'inlinr.com, sans jeton, le dossier personnel
  remplacé par `~`, la même erreur une fois par jour et par machine au plus.
  Un log ne cite jamais le jeton.

## Clés et fuites

- `RELEASE_SIGNING_KEY` (secret GitHub Actions du dépôt, clé privée Ed25519,
  Thibault en garde la copie) signe chaque release ; la clé publique est
  `internal/release/release-key.pub.pem`. La changer se fait en deux temps :
  une CLI et une extension qui portent la nouvelle clé publique sortent
  d'abord.
- Le jeton d'un appareil (`in_d_…`) vit dans `~/.inlinr/config.toml` ; les
  tests passent par `INLINR_HOME` vers un dossier temporaire. Un fichier de
  secrets n'est jamais lu par une IA, quel que soit l'outil (Read, `cat`,
  `grep`) : un `.env`, une clé privée, le `config.toml` d'une vraie machine.
- Une clé par usage, avec le droit le plus petit qui suffit. Rotation tout de
  suite quand elle a pu fuiter (affichée dans un journal, une capture, un fil,
  un commit même effacé), partout où elle vit ; l'ancienne est révoquée après.
  Puis lire les journaux du service et écrire ce qui s'est passé dans le ticket.

## Linear

- Équipe THI, projet Inlinr. Un ticket : titre en français à l'infinitif, 1 à
  4 puces, un label de type (Feature, Improvement, Bug, UI), au besoin un de
  domaine (Marketing, Légal, Ops, SEO). Ni estimation, ni priorité, ni
  assigné. Todo, In Progress, In Review, Done.
- Plan gratuit : 250 tickets non archivés pour tout l'espace. L'archivage se
  tient dans le projet Marketing, pour toutes les apps, sur le mot de
  Thibault : ce dépôt et ses fils n'archivent pas.
- Un rapport va dans un document Linear du projet, pas dans un fichier de plus.

## Fils

- **Un fil, un but**, dit par son titre avec un verbe ; un sujet nouveau ouvre
  un autre fil. Il se ferme quand son but est atteint, ou quand il n'attend
  plus que Thibault depuis deux jours : ce qui reste devient un ticket.
- **Linear relie tout** : un fil qui change du code part d'un ticket, qui
  porte le lien du fil en commentaire ; le fil et la PR citent le ticket. Le
  lien d'un fil ne s'écrit que dans Linear, jamais dans une PR, un commit ni
  un fichier du dépôt.
- **Ce qui attend Thibault vit à un seul endroit** : un ticket Linear au label
  Thibault (une phrase qui dit le geste, le lien du fil, la date limite).
  Jamais une liste tenue dans un doc de ce dépôt ni répétée de message en
  message.
