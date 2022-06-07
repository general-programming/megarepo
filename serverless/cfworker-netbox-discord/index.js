import { INTERESTING_FIELDS, DISPLAY_FIELDS, LABEL_FIELDS, COLOR_MAX } from './constants';

const encoder = new TextEncoder();

addEventListener('fetch', (event) => {
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

    console.log(body);

    // Add status.
    if (body.data['status']) {
        extraFields.push({
            name: 'Status',
            value: body.data.status.label,
            inline: true,
        });
    }

    // Extract cable.
    if (body.data.termination_a && body.data.termination_b) {
        let side_a_name = (body.data.termination_a.device || body.data.termination_a.circuit).display;
        let side_b_name = (body.data.termination_b.device || body.data.termination_b.circuit).display;

        const cable_text = `${side_a_name} <-> ${side_b_name}`;
        extraFields.push({
            name: 'Cable',
            value: cable_text,
            inline: true,
        });
    }

    // Extract link_peer.
    if (body.data.link_peer) {
        let connection_display;

        if (body.data.link_peer.device) {
            connection_display = `${body.data.link_peer.device.display} > ${body.data.link_peer.display}`;
        } else {
            connection_display = `${body.data.link_peer.display}`;
        }

        extraFields.push({
            name: 'Connection',
            value: connection_display,
            inline: true,
        });
    }

    // Add type.
    if (body.data.type) {
        let body_type;

        if (typeof body.data.type === 'string') {
            body_type = body.data.type;
        } else if (typeof body.data.type === 'object') {
            body_type = body.data.type.display || body.data.type.label || body.data.type.value;
        }

        if (body_type) {
            extraFields.push({
                name: 'Type',
                value: body_type,
                inline: true,
            });
        }
    }

    // Extract Assigned Object.
    if (body.data.assigned_object) {
        let assigned_device;

        if (body.data.assigned_object.device) {
            assigned_device = body.data.assigned_object.device.display;
        } else if (body.data.assigned_device.virtual_machine) {
            assigned_device = body.data.assigned_object.virtual_machine.display;
        }

        let assigned_text = body.data.assigned_object.display;
        if (assigned_device) {
            assigned_text = `${assigned_text} [${assigned_device}]`;
        }

        extraFields.push({
            name: 'Assigned To',
            value: assigned_text,
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

    // Merge in label fields.
    for (const k in LABEL_FIELDS) {
        if (body.data[k] && body.data[k].label) {
            extraFields.push({
                name: LABEL_FIELDS[k],
                value: String(body.data[k].label),
                inline: true,
            });
        }
    }

    // Merge in display fields.
    for (const k in DISPLAY_FIELDS) {
        if (body.data[k] && body.data[k].display) {
            extraFields.push({
                name: DISPLAY_FIELDS[k],
                value: String(body.data[k].display),
                inline: true,
            });
        }
    }

    // Add tags if applicable.
    if (body.data.tags && body.data.tags.length != 0) {
        extraFields.push({
            name: 'Tags',
            value: body.data.tags.map((_) => _.display).join(', '),
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
