# Secrets Demo

This demo renders a site-scoped secret named `hello` from a Starlark handler.

```sh
quack deploy demos/secrets secrets-demo
quack secrets set secrets-demo hello "Hello from the secret store"
```

The page checks both `hello` and `hello2`. With only the command above, `hello`
is displayed and `hello2` is shown as missing.

The server secrets root key must be created and unlocked from the admin UI before `quack secrets set` will succeed.
