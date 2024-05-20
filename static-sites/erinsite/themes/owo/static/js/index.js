(function () {
    // Utility
    const randint = (min, max) => {
        try {
            const randomBuffer = new Uint32Array(1);

            window.crypto.getRandomValues(randomBuffer);

            const randomNumber = randomBuffer[0] / (0xffffffff + 1);
            min = Math.ceil(min);
            max = Math.floor(max);
            return Math.floor(randomNumber * (max - min + 1)) + min;
        } catch (e) {
            console.warn("no crypto rng!");
        }

        return Math.round(Math.random() * (max - min) + min);
    };

    const randomElement = (array) => {
        return array[randint(0, array.length - 1)];
    };

    // Random tags
    const tags = [
        "what the fuck is a computer",
        "thanos",
        "obama",
        "certified idiot",
        "don't hire me",
        "holy shit",
        "proudly made in two voice calls",
        "made in china",
        "[object Object]",
        "undefined",
        "left-pad was not a mistake",
        "lol",
        "lole",
        "october 8, 2019",
        "help how do i exit emacs",
        "made with visual studio code and nano",
        ":wq!",
        "i still prefer coding in the backend",
        "Cup.",
        "you can help expand this",
        "[citation needed]",
        "computers were a mistake",
        "oops",
        "woof",
        "meow",
        "awoo",
    ];

    let tagElement = document.getElementById("dynamictag");
    tagElement.textContent = randomElement(tags);

    // Random stripe colors
    // CSS from https://codepen.io/charliewilco/pen/BzAJzE
    const stripes = ["gay", "trans", "bi", "pan", "asex", "nonb", ""];
    let stripe = randomElement(stripes);
    let stripeElement = document.getElementById("background");
    stripeElement.classList.add(stripe);

    // shuffle the webring
    var webring = document.querySelector("#webring");
    for (var i = webring.children.length; i >= 0; i--) {
        webring.appendChild(webring.children[randint(0, i)]);
    }
})();
