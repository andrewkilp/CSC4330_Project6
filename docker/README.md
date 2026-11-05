# Optional database
The template does not need Docker. Persistence tasks add migrations and a database adapter.
Start it later with `docker compose -f docker/compose.yml --profile database up -d`.
Local credentials are development-only; never expose this database publicly.
