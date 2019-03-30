#!/usr/bin/env node
"use strict";

const program = require("commander");

const GPBilling = require("./modules/main");

const exit = () => {
    return setTimeout(() => {
        process.exit(0);
    }, 1000);
};

const exitWithError = (err) => {
    console.error("\n\nAn error occured!\n");
    if (err.response && err.response.obj) console.error(`\nError from the API: ${err.response.obj.error}\n`);
    if (err.stack) console.error(`\nStacktrace:\n${err.stack}\n`);
    return setTimeout(() => {
        process.exit(1);
    }, 1000);
};

const main = async () => {
    console.log("Loading necessary packages, please wait...\n");
    let api = new GPBilling();
    // normally we call `await base.init();` here but
    // due to speed of the runtime, I decided to put
    // it in actions instead. ~linuxgemini

    program
        .version("0.0.1", "-v, --version")
        .description("An ECMAScript CLI for General Programming Payments.");

    program
        .command("login")
        .description("Logs you in.")
        .action(async () => {
            try {
                await api.init();

                if (!api.billingConfig.authCookie) {
                    let { cookie, userObj } = await api.login.start();
                    await api.saveCookie(cookie);
                    console.log(`You are now logged in with email "${userObj.email}".`);
                    exit();
                } else {
                    console.log("You are already logged in!");
                    exit();
                }
            } catch (error) {
                exitWithError(error);
            }
        });

    program
        .command("logout")
        .description("Logs you out.")
        .action(async () => {
            try {
                await api.init();

                if (api.billingConfig.authCookie) {
                    await api.logout.start();
                    await api.nukeCookie();
                    console.log("You are now logged out.");
                    exit();
                } else {
                    console.log("You are already logged out!");
                    exit();
                }
            } catch (error) {
                exitWithError(error);
            }
        });

    program
        .command("register")
        .description("Registers you to the GP Billing database.")
        .action(async () => {
            try {
                await api.init();

                await api.register.start();
                console.log("You are registered! Please check your email for verification.");
                exit();
            } catch (error) {
                exitWithError(error);
            }
        });

    program
        .command("verify")
        .description("Verifies your account on the GP Billing database.")
        .action(async () => {
            try {
                await api.init();

                await api.verify.start();
                console.log("You are verified! You can login now.");
                exit();
            } catch (error) {
                exitWithError(error);
            }
        });

    program.parse(process.argv);

    let NO_COMMAND_SPECIFIED = program.args.length === 0;

    if (NO_COMMAND_SPECIFIED) {
        program.help();
    }
};

main();