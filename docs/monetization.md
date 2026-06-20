# Pistes de monétisation

## UI / observabilité premium

Dashboard standalone connecté au Postgres existant du client — DAG visualization, replay de blueprints, alerting, logs par task.

Modèle freemium : UI basique open-source, rétention longue + alertes + export = payant. Faible friction à l'adoption, valeur visible immédiatement.

C'est le chemin le moins risqué pour commencer.

---

## Gonveyor Cloud (SaaS managé)

On héberge le Postgres + AMQP + scheduler. Le client branche uniquement son worker.

Modèle usage : par blueprint lancé ou par task exécutée. Même modèle que Temporal Cloud sur Temporal.

Gros effort infra, mais c'est le vrai multiplicateur si gonveyor prend de l'ampleur.

---

## Gonveyor Studio

Interface no-code / low-code pour construire des blueprints visuellement et générer le Go en sortie.

Cible : équipes qui ont des devs mais veulent que les PMs/ops puissent lire et modifier les workflows.

Modèle : licence par équipe ou par siège.

---

## Support entreprise

SLA, audit d'installation, formations, customisation. Modèle classique open-source commercialisé.

Peu sexy mais cash réel dès qu'une boîte critique tourne dessus.
