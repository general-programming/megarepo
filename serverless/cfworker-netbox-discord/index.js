import {INTERESTING_FIELDS, COLOR_MAX} from './constants';
import crypto from 'crypto';

addEventListener('fetch', event => {
  event.respondWith(handleRequest(event.request))
});

/**
 * Respond to the request
 * @param {Request} request
 */
async function handleRequest(request) {
  let body_text;
  let body;

  // Try to parse the JSON data.
  try {
    body_text = await request.text();
    body = JSON.parse(body_text);
  } catch(e) {
    return new Response(e, {status: 500})
  }

  // Return a 403 if the HMAC is not good...
  const calculatedHMAC = crypto.createHmac("sha512", HOOK_SECRET).update(body_text).digest("hex");
  const HMACSignature = request.headers.get("X-Hook-Signature");
  if (calculatedHMAC !== HMACSignature) return new Response(JSON.stringify({"error": "bad_hmac", calculated: calculatedHMAC, signature: HMACSignature}), {status: 500})

  // Merge in intersting fields.
  let extraFields = [];
  for (let k in INTERESTING_FIELDS) {
    if (body.data[k]) extraFields.push({
      name: INTERESTING_FIELDS[k],
      value: body.data[k],
      inline: true
    });
  }

  // Create the payload.
  const payload = {
    username: "Nootbox",
    embeds: [{
      title: `${body.model} ${body.event}`,
      // HTTPS only. No exceptions.
      url: `https://${NETBOX_DOMAIN}/extras/changelog/?request_id=${body.request_id}`,
      color: Math.round(Math.random() * COLOR_MAX),
      fields: [
        {name: "User", value: body.username, inline: true},
        {name: "Timestamp", value: body.timestamp, inline: true},
        ...extraFields
      ],
      footer: {
        text: body.request_id
      }
    }]
  };

  // Make the request to Discord and hope it works.
  try {
    await fetch(DISCORD_WEBHOOK, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
  } catch (e) {
    return new Response(e, {status: 500})
  }

  // All is OK!
  return new Response('ok', {status: 200});
}