# reup-goals-backend

## Environment

Core backend:

```sh
DB_HOST=
DB_PORT=5432
DB_USER=
DB_PASSWORD=
DB_NAME=
JWT_SECRET=
CORS_ALLOWED_ORIGINS=https://reupgoals.pro,https://www.reupgoals.pro
```

If `JWT_SECRET` is empty, the backend falls back to the old development secret for compatibility. Production should set a strong value.

`CORS_ALLOWED_ORIGINS` is optional. If it is empty, the backend keeps the previous permissive CORS behavior.
