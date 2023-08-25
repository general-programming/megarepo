(function () {
    // Utility
    const randint = (max) => {
        return Math.round(Math.random() * max);
    };

    const randomElement = (array) => {
        return array[randint(array.length - 1)];
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
    ];

    let tagElement = document.getElementById("dynamictag");
    tagElement.textContent = randomElement(tags);

    // Random stripe colors
    // CSS from https://codepen.io/charliewilco/pen/BzAJzE
    const stripes = ["gay", "trans", "bi", "pan", "asex", "nonb"];
    let stripe = randomElement(stripes);
    let stripeElement = document.getElementById("stripe");
    stripeElement.classList.add(stripe);

    // oh no why would you use a gdpr prompt on a static site
    window.cookieconsent.initialise({
        container: document.getElementById("container"),
        palette: {
            popup: {
                background: "#edeff5",
                text: "#838391",
            },
            button: {
                background: "#4b81e8",
            },
        },
        onStatusChange: function (status) {
            console.log(
                this.hasConsented() ? "enable cookies" : "disable cookies"
            );
        },
        law: {
            regionalLaw: false,
        },
        location: true,
    });
})();
