# Applies migrations before the services start. Run as a one-shot container, not a long-lived one.
FROM migrate/migrate:v4.18.1
COPY migrations /migrations
ENTRYPOINT ["migrate", "-path", "/migrations"]
