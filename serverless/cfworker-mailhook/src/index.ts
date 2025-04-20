/**
 * Welcome to Cloudflare Workers! This is your first worker.
 *
 * - Run `wrangler dev src/index.ts` in your terminal to start a development server
 * - Open a browser tab at http://localhost:8787/ to see your worker in action
 * - Run `wrangler publish src/index.ts --name my-worker` to publish your worker
 *
 * Learn more at https://developers.cloudflare.com/workers/
 */

import PostalMime from "postal-mime";
import puppeteer, { Browser, Puppeteer } from "@cloudflare/puppeteer";
import { md5, formatEmail, toEnrich } from "./utils";
import { HEIGHT_PADDING } from "./consts";
import { discordPush } from "./discord";

export interface Env {
  MYBROWSER: Fetcher;
  BUCKET_BASE: string;
  MY_BUCKET: R2Bucket;
  DISCORD_WEBHOOK?: string;
  EMAIL_QUEUE: Queue<EmailQueueMessage>;
}

interface EmailQueueMessage {
  emailHash: string;
}

async function handleEmail(
  message: ForwardableEmailMessage,
  env: Env,
  ctx: ExecutionContext
): Promise<void> {
  // parse email content
  const rawEmailResposne = new Response(message.raw);
  const rawEmail = await rawEmailResposne.text();

  // md5 the email content and upload the raw contents
  const rawHash = await md5(rawEmail);
  await env.MY_BUCKET.put(`htmlrender_api/${rawHash}.eml`, rawEmail, {
    httpMetadata: {
      contentType: "message/rfc822",
    },
  });
  console.log(`Stored raw email with hash ${rawHash} for queue`);

  await env.EMAIL_QUEUE.send({
    emailHash: rawHash,
  });
  console.log(`Queued email with hash ${rawHash}`);
}

async function processEmail(
  batch: MessageBatch<EmailQueueMessage>,
  env: Env,
  ctx: ExecutionContext
): Promise<void> {
  const browser = await puppeteer.launch(env.MYBROWSER);

  for (const message of batch.messages) {
    const { emailHash } = message.body;

    console.info(`Processing email with hash ${emailHash}`);

    // fetch the raw email from the bucket
    const rawEmailBody = await env.MY_BUCKET.get(
      `htmlrender_api/${emailHash}.eml`
    );
    if (!rawEmailBody) {
      console.error(`No raw email found for hash ${emailHash}`);
      message.ack();
      continue;
    }

    const rawEmail = await rawEmailBody.text();
    const email = await PostalMime.parse(rawEmail);

    // generate the string for email to
    const toString = (email.to || []).map(formatEmail).join(", ");

    // log
    console.log(
      `Received email from ${formatEmail(
        email.from
      )} to ${toString}. (Subject: ${email.subject})`
    );

    // render the email then store the image + html content, and email in an s3 endpoint
    if (!email.html && !email.text) {
      console.error("No email content found");
      message.ack();
      continue;
    }

    // prep for rendering
    const toRender = toEnrich(email);

    // md5 the email content
    const page_hash = await md5(rawEmail);
    await env.MY_BUCKET.put(`htmlrender_api/${page_hash}.html`, toRender, {
      httpMetadata: {
        contentType: "text/html",
      },
    });

    // email render
    const rendered = await renderEmail(browser, toRender);
    await env.MY_BUCKET.put(`htmlrender_api/${page_hash}.png`, rendered, {
      httpMetadata: {
        contentType: "image/png",
      },
    });

    // Set up URLs for the stored content
    const bucketBaseUrl = env.BUCKET_BASE;
    const mailImageUrl = `${bucketBaseUrl}/htmlrender_api/${page_hash}.png`;
    const mailRawUrl = `${bucketBaseUrl}/htmlrender_api/${page_hash}.html`;

    // Push to Discord if webhook URL is configured
    const [pushStatus, pushResponse] = await discordPush(
      env,
      mailImageUrl,
      mailRawUrl,
      email.subject || "[Blank subject]",
      formatEmail(email.from),
      toString
    );

    if (pushStatus !== 204) {
      console.error("Error from Discord:", pushResponse);
      throw new Error("Discord push failed");
    } else {
      console.log("Discord push successful, status code:", pushStatus);
    }
    message.ack();
  }

  await browser.close();
}

/*
 * Renders the email using puppeteer
 *
 * @param env - CF environment variables
 * @param email - HTML content of the email to rende
 *
 */
async function renderEmail(
  browser: Browser,
  email: string
): Promise<Buffer<ArrayBufferLike>> {
  const page = await browser.newPage();
  await page.setContent(email);

  const image = await page.screenshot({
    type: "png",
    fullPage: true,
  });

  await page.close();

  return image;
}

export default {
  email: handleEmail,
  queue: processEmail,
};
