import { COLOR_MAX } from './constants';
import { randIntFromSeed } from './random';

addEventListener('fetch', event => {
  event.respondWith(handleRequest(event.request));
});

/**
 * Respond to the request
 * @param {Request} request
 */
async function handleRequest (request) {
  const requestURL = new URL(request.url);
  const pathKey = requestURL.pathname.split('/')[1];

  // eslint-disable-next-line no-undef
  if (pathKey !== HOOK_SECRET) {
    return new Response(JSON.stringify({ error: 'bad_secret' }), { status: 500 });
  }

  let body;
  let data;

  // Try to parse the JSON data.
  try {
    body = JSON.parse(await request.text());
    data = body;
  } catch (e) {
    return new Response(e, { status: 500 });
  }

  // Generate a color and KV key based on the MAC address.
  const macKey = 'dhcp:' + data.mac.replaceAll(':', '').toLowerCase();
  const color = randIntFromSeed(macKey, COLOR_MAX);

  // Try to grab the last KV data from CF.
  let KVData = await NETHOOKS.get(macKey, 'json');
  if (KVData === null) KVData = { ip: '' };

  // Do nothing if the last IP matches the current IP.
  if (KVData.ip === data.ip) return new Response('', { status: 204 });

  // Else update the IP in the KVData object.
  KVData.ip = data.ip;

  // Create the footer text.
  let footerText = data.dhcp_hostname;
  if (Math.round(Math.random() * 1000) <= 1) {
    footerText = 'you are cute and valid! <3 -' + footerText;
  } else {
    footerText = 'the cute and valid dhcp server running on ' + footerText;
  }

  // Create the payload.
  const payload = {
    username: 'isc-dhcp-server',
    embeds: [{
      title: 'DHCP IP allocated!',
      color: color,
      fields: [
        { name: 'IP', value: data.ip, inline: true },
        { name: 'Hostname', value: data.hostname, inline: true },
        { name: 'MAC', value: data.mac, inline: true }
      ],
      footer: {
        text: footerText
      }
    }]
  };

  // Make the request to Discord and hope it works.
  try {
    // eslint-disable-next-line no-undef
    await fetch(DISCORD_WEBHOOK, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    // Update the CF KV data and return a good state if we are able to post to Discord.
    await NETHOOKS.put(macKey, JSON.stringify(KVData), {
      metadata: {
        expiration: 604800 // Let the data expire every week so things don't get too spammy but not too silent.
      }
    });
    return new Response(JSON.stringify(payload), { status: 200 });
  } catch (e) {
    return new Response(JSON.stringify(e), { status: 500 });
  }
}
