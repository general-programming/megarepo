import {COLOR_MAX} from './constants';

addEventListener('fetch', event => {
  event.respondWith(handleRequest(event.request))
});

/**
 * Respond to the request
 * @param {Request} request
 */
async function handleRequest(request) {
  let requestURL = new URL(request.url);
  let pathKey = requestURL.pathname.split('/')[1];

  if (pathKey !== HOOK_SECRET) {
    return new Response(JSON.stringify({"error": "bad_secret"}), {status: 500});
  }

  let body_text;
  let body;
  let data;

  // Try to parse the JSON data.
  try {
    body_text = await request.text();
    body = JSON.parse(body_text);
    data = body;
  } catch(e) {
    return new Response(e, {status: 500})
  }

  // Handle webhook payloads.
  if (body.result) data = body.result;

  // Create the payload.
  let extraFields = []
  let title = data.tracking_code

  // Add extra fields depending on extra carrier info.
  if (data.tracking_details && data.tracking_details.length > 0) {
    let lastItem = data.tracking_details[data.tracking_details.length - 1]
    if (lastItem.message) extraFields.push({name: 'Latest Message', value: lastItem.message, inline: false})
  }

  if (data.carrier_detail) {
    if (data.carrier_detail.origin_location) extraFields.push({name: 'Origin', value: data.carrier_detail.origin_location, inline: true})
    if (data.carrier_detail.destination_location) extraFields.push({name: 'Destination', value: data.carrier_detail.destination_location, inline: true})
    if (data.carrier_detail.service) title = `${data.carrier_detail.service} - ${data.tracking_code}`
  }

  const payload = {
    username: "easypost hook",
    embeds: [{
      title: title,
      // HTTPS only. No exceptions.
      url: data.public_url,
      color: Math.round(Math.random() * COLOR_MAX),
      fields: [
        {name: 'Carrier', value: data.carrier, inline: true},
        {name: 'Status', value: data.status_detail || data.status, inline: true},
        {name: 'Updated at', value: data.updated_at, inline: true},
        {name: 'ETA', value: data.est_delivery_date || "N/A", inline: true},
        ...extraFields
      ],
      footer: {
        text: data.id
      }
    }]
  };

  // Make the request to Discord and hope it works.
  try {
    let fetchres = await fetch(DISCORD_WEBHOOK, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
    return new Response(JSON.stringify(payload), {status: 200});
  } catch (e) {
    return new Response(JSON.stringify(e), {status: 500})
  }
}