SELECT format('CREATE DATABASE %I', 'logto')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'logto')
\gexec
