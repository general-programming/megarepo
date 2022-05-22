from pyinfra.operations import files, server

ssh_keys = [
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMVk9i7FG7dc9r4ixwAJT7uPLH3UuqbwIgeZ7Ytmnpvv erin",
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIH7FGLD5qvvoCoNpBtj1r6IWNhLh8tauLDUyMLQIYy8i ave@blur",
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIK934lz+iT1NRyo6E6wbTxvfLI04bV0OX7aWuzVoMNPR luna@moon",
]

for key in ssh_keys:
    files.line(
        "/root/.ssh/authorized_keys",
        key,
        present=True,
    )
