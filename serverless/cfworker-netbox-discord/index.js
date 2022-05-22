import { INTERESTING_FIELDS, COLOR_MAX } from './constants';

const encoder = new TextEncoder();

addEventListener('fetch', event => {
    event.respondWith(handleRequest(event.request));
});

function byteStringToUint8Array(byteString) {
    const ui = new Uint8Array(byteString.length);
    for (let i = 0; i < byteString.length; ++i) {
        ui[i] = byteString.charCodeAt(i);
    }
    return ui;
}

/**
 * Respond to the request
 * @param {Request} request
 */
async function handleRequest(request) {
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
    const signature = request.headers.get('X-Hook-Signature');

    const key = await crypto.subtle.importKey(
        'raw',
        encoder.encode(HOOK_SECRET),
        { name: 'HMAC', hash: 'SHA-512' },
        false,
        ['verify'],
    );

    const verified = await crypto.subtle.verify(
        'HMAC',
        key,
        byteStringToUint8Array(signature),
        encoder.encode(bodyText),
    );

    //   if (!verified) {
    //     return new Response(
    //       JSON.stringify({
    //         error: 'bad_hmac',
    //       }),
    //       { status: 500 },
    //     )
    //   }

    // Add status.
    if (body.data['status']) {
        extraFields.push({
            name: 'Status',
            value: body.data.status.label,
            inline: true,
        });
    }

    // Add device name.
    if (body.data['device']) {
        extraFields.push({
            name: 'Device',
            value: body.data.device.display,
            inline: true,
        });
    }

    // Add type.
    if (body.data.type) {
        let body_type;

        if (typeof body.data.type === 'string') {
            body_type = body.data.type;
        } else if (typeof body.data.type === 'object') {
            body_type = body.data.type.label || body.data.type.value;
        }

        extraFields.push({
            name: 'Type',
            value: body_type,
            inline: true,
        });
    }

    // Merge in intersting fields.
    for (const k in INTERESTING_FIELDS) {
        if (body.data[k]) {
            extraFields.push({
                name: INTERESTING_FIELDS[k],
                value: String(body.data[k]),
                inline: true,
            });
        }
    }

    // Add tags if applicable.
    if (body.data.tags && body.data.tags.length != 0) {
        extraFields.push({
            name: 'Tags',
            value: body.data.tags.map(_ => _.display).join(', '),
            inline: true,
        });
    }

    // Add all fields together.
    const fields = [
        { name: 'User', value: body.username, inline: true },
        { name: 'Timestamp', value: body.timestamp, inline: true },
        ...extraFields,
    ];

    // Create the payload.
    let title;

    if (body.data.name) {
        title = `${body.model} '${body.data.name}' ${body.event}`;
    } else {
        title = `${body.model} ${body.event}`;
    }

    const payload = {
        username: 'Nootbox',
        embeds: [
            {
                title: title,
                // HTTPS only. No exceptions.
                // eslint-disable-next-line no-undef
                url: `https://${NETBOX_DOMAIN}/extras/changelog/?request_id=${body.request_id}`,
                color: Math.round(Math.random() * COLOR_MAX),
                fields: fields,
                footer: {
                    text: body.request_id,
                },
            },
        ],
    };

    console.log(body);

    // Make the request to Discord and hope it works.
    try {
        // eslint-disable-next-line no-undef
        const payload_response = await fetch(DISCORD_WEBHOOK, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
        });
        console.log(payload_response);
    } catch (e) {
        return new Response(e, { status: 500 });
    }

    // All is OK!
    return new Response('ok', { status: 200 });
}
