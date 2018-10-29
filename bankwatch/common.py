import re
import hashlib
import os
import datetime
import requests

from plaid import Client as PlaidClient

# Plaid
plaid_client = PlaidClient(
    client_id=os.environ["PLAID_CLIENT_ID"],
    secret=os.environ["PLAID_SECRET"],
    public_key=os.environ["PLAID_PUBLIC_KEY"],
    environment=os.environ["PLAID_ENV"]
)

def get_transactions(item_id, access_token, redis, get_inserted=False):
    transactions_key = "bank:transactions:" + item_id
    done = False
    accounts_loaded = False

    date_from = (datetime.datetime.now() - datetime.timedelta(weeks=4)).strftime("%Y-%m-%d")
    date_to = datetime.datetime.now().strftime("%Y-%m-%d")

    transactions = []
    offset = 0

    while not done and len(transactions) <= offset:
        response = plaid_client.Transactions.get(
            access_token,
            start_date=date_from,
            end_date=date_to,
            offset=len(transactions),
            count=500
        )

        # Asssume no transactions means we've gone through it all.
        if len(response['transactions']) == 0:
            break

        if not accounts_loaded:
            load_accounts(response.get("accounts", {}), redis)
            accounts_loaded = True

        if offset == 0:
            offset = response['total_transactions']

        for transaction in response['transactions']:
            if redis.sismember(transactions_key, transaction["transaction_id"]):
                done = True
                if get_inserted:
                    transactions.append(transaction)
            else:
                transactions.append(transaction)
                
            redis.sadd(transactions_key, transaction["transaction_id"])

    return transactions

def load_accounts(accounts, redis):
    for account in accounts:
        redis.hset("bank:accounts", account["account_id"], account["name"])

def push_plaid_transactions(transactions, redis):
    total_cash = 0
    desctext = ""

    account_groups = {}

    # Fill in account groups
    for transaction in transactions:
        account_id = transaction["account_id"]
        clean_name = transaction['name'].replace("**", "\**")
        total_cash += transaction["amount"]

        if account_id not in account_groups:
            account_groups[account_id] = ""

        account_groups[account_id] += f"[{transaction['date']}] {clean_name} - **${transaction['amount']}**\n"

    # Replace account IDs with names
    for raw_account_name in account_groups.copy().keys():
        account_name = redis.hget("bank:accounts", raw_account_name)
        if account_name:
            account_groups[account_name] = account_groups.pop(raw_account_name)

    # Turn accounts into fields
    account_fields = []
    for account_name, account_data in account_groups.items():
        account_fields.append({
            "name": account_name,
            "value": account_data.rstrip()
        })

    return push_discord_embed(
        title="New transactions",
        description=desctext,
        fields=account_fields,
        footer_text=f"${total_cash:.2f} transacted total. Check Waves for a more complete view."
    )

def push_discord_embed(title, description=None, fields=None, footer_text=None):
    embed_to_push = {
        # Ass bleach pink
        "color": 0xffb9ec,
        "title": title,
    }

    if description:
        embed_to_push["description"] = description

    if fields:
        embed_to_push["fields"] = fields

    if footer_text:
        embed_to_push["footer"] = {
            "text": footer_text
        }

    r = requests.post(os.environ["DISCORD_WEBHOOK"], json={
        "embeds": [
            embed_to_push
        ]
    })

    return r.status_code, r.text
