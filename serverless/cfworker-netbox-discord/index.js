import { INTERESTING_FIELDS, COLOR_MAX } from './constants';
import crypto from 'crypto';

addEventListener('fetch', event => {
  event.respondWith(handleRequest(event.request));
});

/**
 * Respond to the request
 * @param {Request} request
 */
async function handleRequest (request) {
  const bodyText = await request.text();
  const extraFields = [];

  let body;

  // Try to parse the JSON data.
  try {
    body = JSON.parse(bodyText);
  } catch (e) {
    return new Response(e, { status: 500 });
  }

  // Return a 403 if the HMAC is not good...
  // eslint-disable-next-line no-undef
  const calculatedHMAC = crypto.createHmac('sha512', HOOK_SECRET).update(bodyText).digest('hex');
  const HMACSignature = request.headers.get('X-Hook-Signature');
  if (calculatedHMAC !== HMACSignature) {
      return new Response(
          JSON.stringify({
                error: 'bad_hmac',
                calculated: calculatedHMAC,
                signature: HMACSignature
            }),
            { status: 500 }
        );
  }

  // Add status.
  if (body.data['status']) {
    extraFields.push({
        name: 'Status',
        value: body.data.status.label,
        inline: true
    });
  }

  // Add device name.
  if (body.data['device']) {
    extraFields.push({
        name: 'Device',
        value: body.data.device.display,
        inline: true
    });
  }

  // Merge in intersting fields.
  for (const k in INTERESTING_FIELDS) {
    if (body.data[k]) {
      extraFields.push({
        name: INTERESTING_FIELDS[k],
        value: body.data[k],
        inline: true
      });
    }
  }

  // Add tags if applicable.
  if (body.data.tags && body.data.tags.length != 0) {
    extraFields.push({
        name: 'Tags',
        value: body.data.tags.map(_ => _.display).join(", "),
        inline: true,
    });
  }

  // Add all fields together.
  const fields = [
    { name: 'User', value: body.username, inline: true },
    { name: 'Timestamp', value: body.timestamp, inline: true },
    ...extraFields,
  ]

  // Create the payload.
  let title;

  if (body.data.name) {
    title = `${body.model} '${body.data.name}' ${body.event}`;
  } else {
    title = `${body.model} ${body.event}`;
  }


  const payload = {
    username: 'Nootbox',
    embeds: [{
      title: title,
      // HTTPS only. No exceptions.
      // eslint-disable-next-line no-undef
      url: `https://${NETBOX_DOMAIN}/extras/changelog/?request_id=${body.request_id}`,
      color: Math.round(Math.random() * COLOR_MAX),
      fields: fields,
      footer: {
        text: body.request_id
      }
    }]
  };

  console.log(body);

  // Make the request to Discord and hope it works.
  try {
    // eslint-disable-next-line no-undef
    await fetch(DISCORD_WEBHOOK, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
  } catch (e) {
    return new Response(e, { status: 500 });
  }

  // All is OK!
  return new Response('ok', { status: 200 });
}
